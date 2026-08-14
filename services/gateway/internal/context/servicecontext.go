// Package context assembles Gateway's process-scoped dependency graph.
package context

import (
	stdcontext "context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/collaboration/collaborationservice"
	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity/identityservice"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge/knowledgeservice"
	coreapp "github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
	redisresource "github.com/HappyLadySauce/Knowledge-Core/pkg/redis"
	hertztransport "github.com/HappyLadySauce/Knowledge-Core/pkg/transport/hertz"
	gatewayclient "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/client"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/config"
	gatewaymiddleware "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/ratelimit"
	publichttp "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/transport/http"
)

type ServiceContext struct {
	Config        config.Config
	Redis         *redisresource.Resource
	Identity      identityservice.Client
	Knowledge     knowledgeservice.Client
	Collaboration collaborationservice.Client
	Verifier      *coreauth.Verifier
	Limiter       *ratelimit.RedisLimiter
	Middleware    *gatewaymiddleware.Dependencies
	AdminServer   *hertztransport.AdminServer
	PublicServer  *publichttp.Server
}

func NewServiceContext(ctx stdcontext.Context, cfg config.Config, runtime *coreapp.Runtime) (*ServiceContext, error) {
	if ctx == nil || runtime == nil || runtime.Logger == nil || runtime.Health == nil || runtime.Metrics == nil || runtime.Trace == nil {
		return nil, errors.New("create gateway service context: context and initialized runtime are required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("create gateway service context: %w", err)
	}

	cache, err := redisresource.Open(ctx, *cfg.Redis, "rate_limit", runtime.Metrics, runtime.Logger)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddCleanup("redis", func(stdcontext.Context) error { return cache.Close() }); err != nil {
		return nil, errors.Join(err, cache.Close())
	}

	identity, err := gatewayclient.NewIdentity(*cfg.IdentityRPC, runtime.Trace, runtime.Metrics)
	if err != nil {
		return nil, err
	}
	knowledge, err := gatewayclient.NewKnowledge(*cfg.KnowledgeRPC, runtime.Trace, runtime.Metrics)
	if err != nil {
		return nil, err
	}
	collaboration, err := gatewayclient.NewCollaboration(
		*cfg.CollaborationRPC, runtime.Trace, runtime.Metrics,
	)
	if err != nil {
		return nil, err
	}
	verifier, err := coreauth.NewVerifier(cfg.Auth.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("create gateway access-token verifier: %w", err)
	}
	limiter, err := ratelimit.NewRedisLimiter(cache.Client, cfg.RateLimit.KeyPrefix)
	if err != nil {
		return nil, err
	}
	middlewareDependencies, err := gatewaymiddleware.NewDependencies(
		identity, knowledge, collaboration, verifier, limiter, runtime.Health, runtime.Logger, runtime.Requests,
		*cfg.CORS, *cfg.RateLimit, *cfg.Endpoints, cfg.PublicHTTP.TLS.Enabled,
	)
	if err != nil {
		return nil, fmt.Errorf("create gateway HTTP middleware: %w", err)
	}
	if err := addReadinessChecks(runtime.Health, cfg, cache, identity, knowledge, collaboration); err != nil {
		return nil, err
	}

	adminTLS, err := cfg.AdminHTTP.TLS.ServerTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("load gateway admin TLS configuration: %w", err)
	}
	admin, err := hertztransport.NewAdminServer(
		ctx,
		hertztransport.AdminServerConfig{
			ComponentName: "gateway-admin-http",
			LogComponent:  "gateway.admin",
			Options:       *cfg.AdminHTTP,
			TLSConfig:     adminTLS,
		},
		runtime.Health,
		runtime.Metrics,
		runtime.Trace,
		runtime.Logger,
		runtime.Requests,
	)
	if err != nil {
		return nil, err
	}
	// Keep admin readiness available while public traffic drains.
	if err := runtime.AddComponent(admin); err != nil {
		return nil, errors.Join(err, admin.Shutdown(ctx))
	}

	publicTLS, err := cfg.PublicHTTP.TLS.ServerTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("load gateway public TLS configuration: %w", err)
	}
	public, err := publichttp.NewServer(
		ctx, *cfg.PublicHTTP, publicTLS, middlewareDependencies, runtime.Trace, runtime.Metrics,
	)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddComponent(public); err != nil {
		return nil, errors.Join(err, public.Shutdown(ctx))
	}

	runtime.Logger.InfoContext(ctx, "gateway dependencies initialized",
		slog.String("component", "gateway.context"),
		slog.String("event", "dependencies_ready"),
	)
	return &ServiceContext{
		Config: cfg, Redis: cache, Identity: identity, Knowledge: knowledge, Collaboration: collaboration, Verifier: verifier,
		Limiter: limiter, Middleware: middlewareDependencies, AdminServer: admin, PublicServer: public,
	}, nil
}

func (s *ServiceContext) ApplyDynamicConfig(cfg config.Config) error {
	if s == nil || s.Middleware == nil {
		return errors.New("apply gateway dynamic configuration: service context is required")
	}
	return s.Middleware.ApplyDynamic(*cfg.CORS, *cfg.RateLimit, *cfg.Endpoints)
}

func addReadinessChecks(
	registry *health.Registry,
	cfg config.Config,
	cache *redisresource.Resource,
	identity identityservice.Client,
	knowledge knowledgeservice.Client,
	collaboration collaborationservice.Client,
) error {
	return errors.Join(
		registry.AddReadiness("redis", withTimeout(cfg.Redis.ReadTimeout, cache.Ping)),
		registry.AddReadiness("identity", withTimeout(cfg.IdentityRPC.RequestTimeout, func(ctx stdcontext.Context) error {
			response, err := identity.Ping(ctx, &commonv1.PingRequest{})
			if err != nil {
				return fmt.Errorf("ping Identity: %w", err)
			}
			if response == nil || response.Service != "identity" || response.Status != "ready" {
				return errors.New("ping Identity: service is not ready")
			}
			return nil
		})),
		registry.AddReadiness("knowledge", withTimeout(cfg.KnowledgeRPC.RequestTimeout, func(ctx stdcontext.Context) error {
			response, err := knowledge.Ping(ctx, &commonv1.PingRequest{})
			if err != nil {
				return fmt.Errorf("ping Knowledge: %w", err)
			}
			if response == nil || response.Service != "knowledge" || response.Status != "ready" {
				return errors.New("ping Knowledge: service is not ready")
			}
			return nil
		})),
		registry.AddReadiness("collaboration", withTimeout(cfg.CollaborationRPC.RequestTimeout, func(ctx stdcontext.Context) error {
			response, err := collaboration.Ping(ctx, &commonv1.PingRequest{})
			if err != nil {
				return fmt.Errorf("ping Collaboration: %w", err)
			}
			if response == nil || response.Service != "collaboration" || response.Status != "ready" {
				return errors.New("ping Collaboration: service is not ready")
			}
			return nil
		})),
	)
}

func withTimeout(timeout time.Duration, check health.Check) health.Check {
	return func(ctx stdcontext.Context) error {
		checkCtx, cancel := stdcontext.WithTimeout(ctx, timeout)
		defer cancel()
		return check(checkCtx)
	}
}

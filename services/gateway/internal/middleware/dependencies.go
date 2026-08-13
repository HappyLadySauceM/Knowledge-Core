package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync/atomic"
	"time"

	collaborationv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/collaboration"
	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	identityv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge/knowledgeservice"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
	corelog "github.com/HappyLadySauce/Knowledge-Core/pkg/log"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/config"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/kitex/client/callopt"
)

const (
	dependenciesKey = "gateway.dependencies"
	principalKey    = "gateway.principal"
	accessTokenKey  = "gateway.access_token"
	clientIPKey     = "gateway.client_ip"
)

type IdentityClient interface {
	Ping(context.Context, *commonv1.PingRequest, ...callopt.Option) (*commonv1.PingResponse, error)
	Register(context.Context, *identityv1.RegisterRequest, ...callopt.Option) (*identityv1.User, error)
	Authenticate(context.Context, *identityv1.AuthenticateRequest, ...callopt.Option) (*identityv1.Authentication, error)
	GetCurrentUser(context.Context, *identityv1.CurrentUserRequest, ...callopt.Option) (*identityv1.User, error)
}

type TokenVerifier interface {
	Verify(string) (coreauth.Principal, error)
}

type RateLimiter interface {
	Consume(context.Context, string, string, time.Time, time.Duration, int64) (bool, time.Duration, error)
}

type Dependencies struct {
	Identity       IdentityClient
	Knowledge      knowledgeservice.Client
	Collaboration  CollaborationClient
	Verifier       TokenVerifier
	Limiter        RateLimiter
	Health         *health.Registry
	Logger         *slog.Logger
	RequestLogs    *corelog.RequestControl
	AllowedOrigins map[string]struct{}
	TrustedProxies []*net.IPNet
	RateLimit      config.RateLimitOptions
	Endpoints      config.EndpointOptions
	Secure         bool
	Now            func() time.Time
	dynamic        atomic.Pointer[dynamicConfig]
}

type dynamicConfig struct {
	allowedOrigins map[string]struct{}
	trustedProxies []*net.IPNet
	rateLimit      config.RateLimitOptions
	endpoints      config.EndpointOptions
}

type CollaborationClient interface {
	Ping(context.Context, *commonv1.PingRequest, ...callopt.Option) (*commonv1.PingResponse, error)
	CreateSession(context.Context, *collaborationv1.CreateSessionRequest, ...callopt.Option) (*collaborationv1.CollaborationSession, error)
	ListVersions(context.Context, *collaborationv1.ListVersionsRequest, ...callopt.Option) (*collaborationv1.VersionPage, error)
	CreateVersion(context.Context, *collaborationv1.CreateVersionRequest, ...callopt.Option) (*collaborationv1.Version, error)
	GetVersion(context.Context, *collaborationv1.GetVersionRequest, ...callopt.Option) (*collaborationv1.VersionDetail, error)
	RestoreVersion(context.Context, *collaborationv1.RestoreVersionRequest, ...callopt.Option) (*collaborationv1.Version, error)
}

func NewDependencies(
	identity IdentityClient,
	knowledge knowledgeservice.Client,
	collaboration CollaborationClient,
	verifier TokenVerifier,
	limiter RateLimiter,
	healthRegistry *health.Registry,
	logger *slog.Logger,
	requestLogs *corelog.RequestControl,
	cors config.CORSOptions,
	rateLimit config.RateLimitOptions,
	endpoints config.EndpointOptions,
	secure bool,
) (*Dependencies, error) {
	if identity == nil || knowledge == nil || collaboration == nil || verifier == nil || limiter == nil || healthRegistry == nil || logger == nil {
		return nil, errors.New("create gateway middleware dependencies: upstream clients, verifier, limiter, health, and logger are required")
	}
	if err := cors.Validate(); err != nil {
		return nil, err
	}
	if err := rateLimit.Validate(); err != nil {
		return nil, err
	}
	if err := endpoints.Validate(); err != nil {
		return nil, err
	}
	trustedProxies, err := cors.ParsedTrustedProxyCIDRs()
	if err != nil {
		return nil, err
	}
	allowedOrigins := make(map[string]struct{}, len(cors.AllowedOrigins))
	for _, origin := range cors.AllowedOrigins {
		allowedOrigins[origin] = struct{}{}
	}
	dependencies := &Dependencies{
		Identity: identity, Knowledge: knowledge, Collaboration: collaboration,
		Verifier: verifier, Limiter: limiter, Health: healthRegistry, Logger: logger, RequestLogs: requestLogs,
		AllowedOrigins: allowedOrigins, TrustedProxies: trustedProxies, RateLimit: rateLimit, Endpoints: endpoints, Secure: secure,
		Now: time.Now,
	}
	dependencies.dynamic.Store(&dynamicConfig{allowedOrigins: allowedOrigins, trustedProxies: trustedProxies, rateLimit: rateLimit, endpoints: endpoints})
	return dependencies, nil
}

func (d *Dependencies) ApplyDynamic(cors config.CORSOptions, rateLimit config.RateLimitOptions, endpoints config.EndpointOptions) error {
	if err := errors.Join(cors.Validate(), rateLimit.Validate(), endpoints.Validate()); err != nil {
		return err
	}
	trustedProxies, err := cors.ParsedTrustedProxyCIDRs()
	if err != nil {
		return err
	}
	allowedOrigins := make(map[string]struct{}, len(cors.AllowedOrigins))
	for _, origin := range cors.AllowedOrigins {
		allowedOrigins[origin] = struct{}{}
	}
	d.dynamic.Store(&dynamicConfig{allowedOrigins: allowedOrigins, trustedProxies: trustedProxies, rateLimit: rateLimit, endpoints: endpoints})
	return nil
}

func (d *Dependencies) Dynamic() (map[string]struct{}, []*net.IPNet, config.RateLimitOptions, config.EndpointOptions) {
	value := d.dynamic.Load()
	if value == nil {
		return d.AllowedOrigins, d.TrustedProxies, d.RateLimit, d.Endpoints
	}
	return value.allowedOrigins, value.trustedProxies, value.rateLimit, value.endpoints
}

func (d *Dependencies) EndpointOptions() config.EndpointOptions {
	_, _, _, endpoints := d.Dynamic()
	return endpoints
}

func Inject(dependencies *Dependencies) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		if dependencies == nil {
			WriteError(ctx, request, ErrInternal)
			return
		}
		request.Set(dependenciesKey, dependencies)
		request.Next(ctx)
	}
}

func FromRequest(request *app.RequestContext) (*Dependencies, bool) {
	if request == nil {
		return nil, false
	}
	value, exists := request.Get(dependenciesKey)
	dependencies, ok := value.(*Dependencies)
	return dependencies, exists && ok && dependencies != nil
}

func Principal(request *app.RequestContext) (coreauth.Principal, bool) {
	if request == nil {
		return coreauth.Principal{}, false
	}
	value, exists := request.Get(principalKey)
	principal, ok := value.(coreauth.Principal)
	return principal, exists && ok && principal.UserID > 0
}

func AccessToken(request *app.RequestContext) (string, bool) {
	if request == nil {
		return "", false
	}
	value, exists := request.Get(accessTokenKey)
	token, ok := value.(string)
	return token, exists && ok && token != ""
}

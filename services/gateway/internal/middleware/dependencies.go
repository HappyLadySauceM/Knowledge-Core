package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	identityv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/config"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/kitex/client/callopt"
)

const (
	dependenciesKey = "gateway.dependencies"
	principalKey    = "gateway.principal"
	accessTokenKey  = "gateway.access_token"
	currentUserKey  = "gateway.current_user"
	clientIPKey     = "gateway.client_ip"
)

type IdentityClient interface {
	Ping(context.Context, *commonv1.PingRequest, ...callopt.Option) (*commonv1.PingResponse, error)
	Register(context.Context, *identityv1.RegisterRequest, ...callopt.Option) (*identityv1.User, error)
	Authenticate(context.Context, *identityv1.AuthenticateRequest, ...callopt.Option) (*identityv1.Authentication, error)
	GetUser(context.Context, *identityv1.GetUserRequest, ...callopt.Option) (*identityv1.User, error)
}

type TokenVerifier interface {
	Verify(string) (coreauth.Principal, error)
}

type RateLimiter interface {
	Consume(context.Context, string, string, time.Time, time.Duration, int64) (bool, time.Duration, error)
}

type Dependencies struct {
	Identity       IdentityClient
	Verifier       TokenVerifier
	Limiter        RateLimiter
	Health         *health.Registry
	Logger         *slog.Logger
	AllowedOrigins map[string]struct{}
	TrustedProxies []*net.IPNet
	RateLimit      config.RateLimitOptions
	Secure         bool
	Now            func() time.Time
}

func NewDependencies(
	identity IdentityClient,
	verifier TokenVerifier,
	limiter RateLimiter,
	healthRegistry *health.Registry,
	logger *slog.Logger,
	cors config.CORSOptions,
	rateLimit config.RateLimitOptions,
	secure bool,
) (*Dependencies, error) {
	if identity == nil || verifier == nil || limiter == nil || healthRegistry == nil || logger == nil {
		return nil, errors.New("create gateway middleware dependencies: identity, verifier, limiter, health, and logger are required")
	}
	if err := cors.Validate(); err != nil {
		return nil, err
	}
	if err := rateLimit.Validate(); err != nil {
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
	return &Dependencies{
		Identity: identity, Verifier: verifier, Limiter: limiter, Health: healthRegistry, Logger: logger,
		AllowedOrigins: allowedOrigins, TrustedProxies: trustedProxies, RateLimit: rateLimit, Secure: secure,
		Now: time.Now,
	}, nil
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

func CurrentUser(request *app.RequestContext) (*identityv1.User, bool) {
	if request == nil {
		return nil, false
	}
	value, exists := request.Get(currentUserKey)
	user, ok := value.(*identityv1.User)
	return user, exists && ok && user != nil
}

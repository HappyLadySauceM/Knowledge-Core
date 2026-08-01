package middleware

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	identityv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/config"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/kitex/client/callopt"
)

type identityClientStub struct {
	user       *identityv1.User
	getUserErr error
	tokenSeen  string
}

func (s *identityClientStub) Ping(context.Context, *commonv1.PingRequest, ...callopt.Option) (*commonv1.PingResponse, error) {
	return &commonv1.PingResponse{Service: "identity", Status: "ready"}, nil
}
func (s *identityClientStub) Register(context.Context, *identityv1.RegisterRequest, ...callopt.Option) (*identityv1.User, error) {
	return s.user, nil
}
func (s *identityClientStub) Authenticate(context.Context, *identityv1.AuthenticateRequest, ...callopt.Option) (*identityv1.Authentication, error) {
	return nil, nil
}
func (s *identityClientStub) GetUser(ctx context.Context, _ *identityv1.GetUserRequest, _ ...callopt.Option) (*identityv1.User, error) {
	s.tokenSeen = coreauth.AccessToken(ctx)
	return s.user, s.getUserErr
}

type limiterStub struct {
	allowed bool
	retry   time.Duration
	err     error
}

func (s limiterStub) Consume(context.Context, string, string, time.Time, time.Duration, int64) (bool, time.Duration, error) {
	return s.allowed, s.retry, s.err
}

func TestAuthenticationVerifiesAndRefreshesCurrentUser(t *testing.T) {
	keys, err := coreauth.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	issuer, _ := coreauth.NewIssuer(keys.PrivateKey, 15*time.Minute)
	verifier, _ := coreauth.NewVerifier(keys.PublicKey)
	issued, err := issuer.Issue(coreauth.Principal{UserID: 7, Role: "user", TokenVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	identity := &identityClientStub{user: &identityv1.User{Id: 7, Role: "user", TokenVersion: 2}}
	dependencies := testDependencies(identity, verifier, limiterStub{allowed: true})
	request := app.NewContext(0)
	request.Request.Header.Set("Authorization", "Bearer "+issued.Value)
	reached := false
	request.SetHandlers(app.HandlersChain{
		Inject(dependencies),
		OptionalAuthentication(),
		RequireAuthenticated(),
		func(context.Context, *app.RequestContext) { reached = true },
	})
	request.Next(context.Background())
	if !reached || identity.tokenSeen != issued.Value {
		t.Fatalf("reached = %v, tokenSeen = %q", reached, identity.tokenSeen)
	}
	if user, ok := CurrentUser(request); !ok || user.Id != 7 {
		t.Fatalf("CurrentUser() = %#v, %v", user, ok)
	}
}

func TestOptionalAuthenticationRejectsInvalidBearer(t *testing.T) {
	keys, _ := coreauth.GenerateKeyPair()
	verifier, _ := coreauth.NewVerifier(keys.PublicKey)
	request := app.NewContext(0)
	request.Request.Header.Set("Authorization", "Bearer invalid")
	request.SetHandlers(app.HandlersChain{
		Inject(testDependencies(&identityClientStub{}, verifier, limiterStub{allowed: true})),
		OptionalAuthentication(),
	})
	request.Next(context.Background())
	if request.Response.StatusCode() != consts.StatusUnauthorized {
		t.Fatalf("status = %d", request.Response.StatusCode())
	}
	if got := string(request.Response.Header.Peek("WWW-Authenticate")); got != "Bearer" {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
}

func TestRateLimitFailsClosedAndReportsRetry(t *testing.T) {
	request := app.NewContext(0)
	request.SetHandlers(app.HandlersChain{
		Inject(testDependencies(&identityClientStub{}, verifierStub{}, limiterStub{err: errors.New("redis unavailable")})),
		GlobalRateLimit(),
	})
	request.Next(context.Background())
	if request.Response.StatusCode() != consts.StatusServiceUnavailable {
		t.Fatalf("fail-closed status = %d", request.Response.StatusCode())
	}

	request = app.NewContext(0)
	request.SetHandlers(app.HandlersChain{
		Inject(testDependencies(&identityClientStub{}, verifierStub{}, limiterStub{allowed: false, retry: 1500 * time.Millisecond})),
		GlobalRateLimit(),
	})
	request.Next(context.Background())
	if request.Response.StatusCode() != consts.StatusTooManyRequests {
		t.Fatalf("limited status = %d", request.Response.StatusCode())
	}
	if got := string(request.Response.Header.Peek("Retry-After")); got != "2" {
		t.Fatalf("Retry-After = %q", got)
	}
}

func TestCORSRequiresExactConfiguredOrigin(t *testing.T) {
	dependencies := testDependencies(&identityClientStub{}, verifierStub{}, limiterStub{allowed: true})
	dependencies.AllowedOrigins = map[string]struct{}{"https://example.com": {}}

	request := app.NewContext(0)
	request.Request.Header.Set("Origin", "https://evil.example")
	request.SetHandlers(app.HandlersChain{Inject(dependencies), CORS()})
	request.Next(context.Background())
	if request.Response.StatusCode() != consts.StatusForbidden {
		t.Fatalf("disallowed status = %d", request.Response.StatusCode())
	}

	request = app.NewContext(0)
	request.Request.Header.Set("Origin", "https://example.com")
	request.Request.Header.Set("Access-Control-Request-Method", "POST")
	request.Request.Header.SetMethod(consts.MethodOptions)
	request.SetHandlers(app.HandlersChain{Inject(dependencies), CORS()})
	request.Next(context.Background())
	if request.Response.StatusCode() != consts.StatusNoContent {
		t.Fatalf("preflight status = %d", request.Response.StatusCode())
	}
	if got := string(request.Response.Header.Peek("Access-Control-Allow-Origin")); got != "https://example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
}

type verifierStub struct{}

func (verifierStub) Verify(string) (coreauth.Principal, error) {
	return coreauth.Principal{}, errors.New("invalid")
}

func testDependencies(identity IdentityClient, verifier TokenVerifier, limiter RateLimiter) *Dependencies {
	return &Dependencies{
		Identity: identity, Verifier: verifier, Limiter: limiter, Health: health.NewRegistry(),
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		RateLimit: config.RateLimitOptions{Window: time.Minute, GlobalLimit: 300, AuthLimit: 20},
		Now:       time.Now,
	}
}

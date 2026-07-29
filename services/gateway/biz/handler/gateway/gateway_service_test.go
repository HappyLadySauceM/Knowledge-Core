package gateway_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	auth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	"github.com/HappyLadySauce/Knowledge-Core/internal/health"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	identityrpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity/identityservice"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/router"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/kitex/client/callopt"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

func TestHealthRoutes(t *testing.T) {
	registry := health.NewRegistry()
	registry.SetServing(true)
	engine := server.New()
	registerRoutes(t, engine, staticVerifier{}, middleware.RuntimeDependencies{Health: registry, Identity: &fakeIdentityClient{}})

	live := ut.PerformRequest(engine.Engine, "GET", "/health/live", nil)
	if live.Code != 200 || !strings.Contains(live.Body.String(), `"status":"live"`) {
		t.Fatalf("live response = %d %s", live.Code, live.Body.String())
	}
	ready := ut.PerformRequest(engine.Engine, "GET", "/health/ready", nil)
	if ready.Code != 200 || !strings.Contains(ready.Body.String(), `"status":"ready"`) {
		t.Fatalf("ready response = %d %s", ready.Code, ready.Body.String())
	}

	registry.SetServing(false)
	notReady := ut.PerformRequest(engine.Engine, "GET", "/health/ready", nil)
	if notReady.Code != 503 || !strings.Contains(notReady.Body.String(), `"code":10001`) {
		t.Fatalf("not-ready response = %d %s", notReady.Code, notReady.Body.String())
	}
}

func TestAuthenticationRoutes(t *testing.T) {
	client := &fakeIdentityClient{user: testRPCUser()}
	engine, token := newAuthenticatedEngine(t, client)

	registerBody := `{"username":"alice","email":"alice@example.com","password":"correct-password"}`
	registered := performJSON(engine, "POST", "/api/v1/auth/register", registerBody)
	if registered.Code != 201 || !strings.Contains(registered.Body.String(), `"username":"alice"`) {
		t.Fatalf("register response = %d %s", registered.Code, registered.Body.String())
	}

	loginBody := `{"identifier":"alice","password":"correct-password"}`
	loggedIn := performJSON(engine, "POST", "/api/v1/auth/login", loginBody)
	if loggedIn.Code != 200 || !strings.Contains(loggedIn.Body.String(), `"token_type":"Bearer"`) || !strings.Contains(loggedIn.Body.String(), token) {
		t.Fatalf("login response = %d %s", loggedIn.Code, loggedIn.Body.String())
	}

	currentUser := ut.PerformRequest(
		engine.Engine,
		"GET",
		"/api/v1/users/me",
		nil,
		ut.Header{Key: "Authorization", Value: "Bearer " + token},
	)
	if currentUser.Code != 200 || !strings.Contains(currentUser.Body.String(), `"email":"alice@example.com"`) {
		t.Fatalf("current-user response = %d %s", currentUser.Code, currentUser.Body.String())
	}
}

func TestCurrentUserRejectsInvalidOrRevokedToken(t *testing.T) {
	client := &fakeIdentityClient{user: testRPCUser()}
	engine, token := newAuthenticatedEngine(t, client)

	missing := ut.PerformRequest(engine.Engine, "GET", "/api/v1/users/me", nil)
	if missing.Code != 401 || !strings.Contains(missing.Body.String(), `"code":10003`) {
		t.Fatalf("missing-token response = %d %s", missing.Code, missing.Body.String())
	}
	invalid := ut.PerformRequest(
		engine.Engine,
		"GET",
		"/api/v1/users/me",
		nil,
		ut.Header{Key: "Authorization", Value: "Bearer invalid-token"},
	)
	if invalid.Code != 401 {
		t.Fatalf("invalid-token response = %d %s", invalid.Code, invalid.Body.String())
	}

	client.user.TokenVersion++
	revoked := ut.PerformRequest(
		engine.Engine,
		"GET",
		"/api/v1/users/me",
		nil,
		ut.Header{Key: "Authorization", Value: "Bearer " + token},
	)
	if revoked.Code != 401 {
		t.Fatalf("revoked-token response = %d %s", revoked.Code, revoked.Body.String())
	}
}

func TestRegisterMapsIdentityConflict(t *testing.T) {
	client := &fakeIdentityClient{
		user: testRPCUser(),
		err:  kerrors.NewBizStatusError(identityrpc.CodeConflict, "username already exists"),
	}
	engine, _ := newAuthenticatedEngine(t, client)
	response := performJSON(
		engine,
		"POST",
		"/api/v1/auth/register",
		`{"username":"alice","email":"alice@example.com","password":"correct-password"}`,
	)
	if response.Code != 409 || !strings.Contains(response.Body.String(), `"code":10004`) {
		t.Fatalf("conflict response = %d %s", response.Code, response.Body.String())
	}
}

func newAuthenticatedEngine(t *testing.T, client *fakeIdentityClient) (*server.Hertz, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	issuer, err := auth.NewIssuer(base64.StdEncoding.EncodeToString(privateKey), 15*time.Minute)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	issued, err := issuer.Issue(auth.Principal{
		UserID:       client.user.Id,
		Role:         client.user.Role,
		TokenVersion: client.user.TokenVersion,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	client.authentication = &identityrpc.Authentication{
		User:          client.user,
		AccessToken:   issued.Value,
		ExpiresAtUnix: issued.ExpiresAt.Unix(),
	}
	verifier, err := auth.NewVerifier(base64.StdEncoding.EncodeToString(publicKey))
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	registry := health.NewRegistry()
	registry.SetServing(true)
	engine := server.New()
	registerRoutes(t, engine, verifier, middleware.RuntimeDependencies{Health: registry, Identity: client})
	return engine, issued.Value
}

func registerRoutes(t *testing.T, engine *server.Hertz, verifier middleware.TokenVerifier, dependencies middleware.RuntimeDependencies) {
	t.Helper()
	tracing := func(ctx context.Context, request *app.RequestContext) { request.Next(ctx) }
	if err := router.Register(engine, router.Config{
		Logger:       slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Tracing:      tracing,
		Verifier:     verifier,
		Dependencies: dependencies,
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
}

func performJSON(engine *server.Hertz, method, path, body string) *ut.ResponseRecorder {
	return ut.PerformRequest(
		engine.Engine,
		method,
		path,
		&ut.Body{Body: strings.NewReader(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	)
}

func testRPCUser() *identityrpc.User {
	now := time.Now().UTC()
	return &identityrpc.User{
		Id:            42,
		Username:      "alice",
		Email:         "alice@example.com",
		Role:          "user",
		Status:        "active",
		TokenVersion:  1,
		CreatedAtUnix: now.Unix(),
		UpdatedAtUnix: now.Unix(),
	}
}

type fakeIdentityClient struct {
	user           *identityrpc.User
	authentication *identityrpc.Authentication
	err            error
}

type staticVerifier struct{}

func (staticVerifier) Verify(string) (auth.Principal, error) {
	return auth.Principal{}, nil
}

func (f *fakeIdentityClient) Ping(context.Context, *common.PingRequest, ...callopt.Option) (*common.PingResponse, error) {
	return &common.PingResponse{Service: "identity", Status: "ok"}, f.err
}

func (f *fakeIdentityClient) Register(context.Context, *identityrpc.RegisterRequest, ...callopt.Option) (*identityrpc.User, error) {
	return f.user, f.err
}

func (f *fakeIdentityClient) Authenticate(context.Context, *identityrpc.AuthenticateRequest, ...callopt.Option) (*identityrpc.Authentication, error) {
	return f.authentication, f.err
}

func (f *fakeIdentityClient) GetUser(context.Context, *identityrpc.GetUserRequest, ...callopt.Option) (*identityrpc.User, error) {
	return f.user, f.err
}

var _ identityservice.Client = (*fakeIdentityClient)(nil)

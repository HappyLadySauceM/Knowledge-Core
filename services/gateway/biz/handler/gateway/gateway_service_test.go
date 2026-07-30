package gateway_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	auth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	"github.com/HappyLadySauce/Knowledge-Core/internal/cache"
	"github.com/HappyLadySauce/Knowledge-Core/internal/health"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	identityrpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity/identityservice"
	knowledgerpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge/knowledgeservice"
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
	assertResponseMetadata(t, live)
	ready := ut.PerformRequest(engine.Engine, "GET", "/health/ready", nil)
	if ready.Code != 200 || !strings.Contains(ready.Body.String(), `"status":"ready"`) {
		t.Fatalf("ready response = %d %s", ready.Code, ready.Body.String())
	}
	assertResponseMetadata(t, ready)

	registry.SetServing(false)
	notReady := ut.PerformRequest(engine.Engine, "GET", "/health/ready", nil)
	if notReady.Code != 503 || !strings.Contains(notReady.Body.String(), `"code":10001`) {
		t.Fatalf("not-ready response = %d %s", notReady.Code, notReady.Body.String())
	}
	assertResponseMetadata(t, notReady)
}

func TestAuthenticationRoutes(t *testing.T) {
	client := &fakeIdentityClient{user: testRPCUser()}
	engine, token := newAuthenticatedEngine(t, client)

	registerBody := `{"username":"alice","email":"alice@example.com","password":"correct-password"}`
	registered := performJSON(engine, "POST", "/api/v1/auth/register", registerBody)
	if registered.Code != 201 || !strings.Contains(registered.Body.String(), `"username":"alice"`) {
		t.Fatalf("register response = %d %s", registered.Code, registered.Body.String())
	}
	assertResponseMetadata(t, registered)

	loginBody := `{"identifier":"alice","password":"correct-password"}`
	loggedIn := performJSON(engine, "POST", "/api/v1/auth/login", loginBody)
	if loggedIn.Code != 200 || !strings.Contains(loggedIn.Body.String(), `"token_type":"Bearer"`) || !strings.Contains(loggedIn.Body.String(), token) {
		t.Fatalf("login response = %d %s", loggedIn.Code, loggedIn.Body.String())
	}
	assertResponseMetadata(t, loggedIn)

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
	assertResponseMetadata(t, currentUser)
	if client.getUserCalls != 1 || client.accessToken != token {
		t.Fatalf("GetUser calls = %d, access token matches = %t", client.getUserCalls, client.accessToken == token)
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
	if response.Code != 409 || !strings.Contains(response.Body.String(), `"code":20002`) {
		t.Fatalf("conflict response = %d %s", response.Code, response.Body.String())
	}
}

func TestDocumentRoutesExposePublicReadsAndProtectStudio(t *testing.T) {
	client := &fakeIdentityClient{user: testRPCUser()}
	engine, token := newAuthenticatedEngine(t, client)
	publicList := ut.PerformRequest(engine.Engine, "GET", "/api/v1/documents", nil)
	if publicList.Code != 200 || !strings.Contains(publicList.Body.String(), `"code":0`) {
		t.Fatalf("public document list = %d %s", publicList.Code, publicList.Body.String())
	}
	studioList := ut.PerformRequest(engine.Engine, "GET", "/api/v1/studio/documents", nil,
		ut.Header{Key: "Authorization", Value: "Bearer " + token})
	if studioList.Code != 403 || !strings.Contains(studioList.Body.String(), `"code":10005`) {
		t.Fatalf("studio document list = %d %s", studioList.Code, studioList.Body.String())
	}
}

func TestMiddlewareOrderingStopsRequestsAtTheExpectedBoundary(t *testing.T) {
	identity := &fakeIdentityClient{user: testRPCUser()}
	cacheStore := &fakeCache{}
	verifier := &countingVerifier{principal: auth.Principal{
		UserID: identity.user.Id, Role: identity.user.Role, TokenVersion: identity.user.TokenVersion,
	}}
	registry := health.NewRegistry()
	registry.SetServing(true)
	config, err := middleware.LoadConfig(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	config.RateLimit.GlobalLimit = 1
	engine := server.New()
	tracing := func(ctx context.Context, request *app.RequestContext) { request.Next(ctx) }
	if err := router.Register(engine, router.Config{
		Logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Tracing:  tracing,
		Verifier: verifier,
		Dependencies: middleware.RuntimeDependencies{
			Health: registry, Cache: cacheStore, Identity: identity, Knowledge: &fakeKnowledgeClient{},
		},
		Middleware: config,
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	deniedByCORS := ut.PerformRequest(engine.Engine, "GET", "/api/v1/users/me", nil,
		ut.Header{Key: "Origin", Value: "https://attacker.example"},
		ut.Header{Key: "Authorization", Value: "Bearer verified-token"})
	if deniedByCORS.Code != 403 || cacheStore.incrementCalls() != 0 || verifier.calls != 0 {
		t.Fatalf("CORS response = %d, cache calls = %d, verifier calls = %d", deniedByCORS.Code, cacheStore.incrementCalls(), verifier.calls)
	}

	allowed := ut.PerformRequest(engine.Engine, "GET", "/api/v1/users/me", nil,
		ut.Header{Key: "Authorization", Value: "Bearer verified-token"})
	if allowed.Code != 200 || cacheStore.incrementCalls() != 1 || verifier.calls != 1 || identity.getUserCalls != 1 {
		t.Fatalf("allowed response = %d, cache = %d, verifier = %d, Identity = %d",
			allowed.Code, cacheStore.incrementCalls(), verifier.calls, identity.getUserCalls)
	}

	deniedByRateLimit := ut.PerformRequest(engine.Engine, "GET", "/api/v1/users/me", nil,
		ut.Header{Key: "Authorization", Value: "Bearer verified-token"})
	if deniedByRateLimit.Code != 429 || verifier.calls != 1 || identity.getUserCalls != 1 {
		t.Fatalf("rate response = %d, verifier = %d, Identity = %d", deniedByRateLimit.Code, verifier.calls, identity.getUserCalls)
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
	if dependencies.Cache == nil {
		dependencies.Cache = &fakeCache{}
	}
	if dependencies.Knowledge == nil {
		dependencies.Knowledge = &fakeKnowledgeClient{}
	}
	middlewareConfig, err := middleware.LoadConfig(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	tracing := func(ctx context.Context, request *app.RequestContext) { request.Next(ctx) }
	if err := router.Register(engine, router.Config{
		Logger:       slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Tracing:      tracing,
		Verifier:     verifier,
		Dependencies: dependencies,
		Middleware:   middlewareConfig,
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
	getUserCalls   int
	accessToken    string
}

type staticVerifier struct{}

func (staticVerifier) Verify(string) (auth.Principal, error) {
	return auth.Principal{}, nil
}

type countingVerifier struct {
	principal auth.Principal
	calls     int
}

func (v *countingVerifier) Verify(string) (auth.Principal, error) {
	v.calls++
	return v.principal, nil
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

func (f *fakeIdentityClient) GetUser(ctx context.Context, _ *identityrpc.GetUserRequest, _ ...callopt.Option) (*identityrpc.User, error) {
	f.getUserCalls++
	f.accessToken = auth.AccessToken(ctx)
	return f.user, f.err
}

var _ identityservice.Client = (*fakeIdentityClient)(nil)

type fakeKnowledgeClient struct {
	err error
}

func (f *fakeKnowledgeClient) Ping(context.Context, *common.PingRequest, ...callopt.Option) (*common.PingResponse, error) {
	return &common.PingResponse{Service: "knowledge", Status: "ok"}, f.err
}

func (f *fakeKnowledgeClient) ListPublishedDocuments(context.Context, *knowledgerpc.DocumentListRequest, ...callopt.Option) (*knowledgerpc.DocumentList, error) {
	return &knowledgerpc.DocumentList{Items: make([]*knowledgerpc.Document, 0)}, f.err
}

func (f *fakeKnowledgeClient) ListDocuments(context.Context, *knowledgerpc.DocumentListRequest, ...callopt.Option) (*knowledgerpc.DocumentList, error) {
	return &knowledgerpc.DocumentList{Items: make([]*knowledgerpc.Document, 0)}, f.err
}

func (f *fakeKnowledgeClient) GetPublishedDocument(context.Context, *knowledgerpc.DocumentIDRequest, ...callopt.Option) (*knowledgerpc.DocumentDetail, error) {
	return nil, f.err
}

func (f *fakeKnowledgeClient) CreateDocument(context.Context, *knowledgerpc.CreateDocumentRequest, ...callopt.Option) (*knowledgerpc.DocumentDetail, error) {
	return nil, f.err
}

func (f *fakeKnowledgeClient) GetDocument(context.Context, *knowledgerpc.DocumentIDRequest, ...callopt.Option) (*knowledgerpc.DocumentDetail, error) {
	return nil, f.err
}

func (f *fakeKnowledgeClient) UpdateDocument(context.Context, *knowledgerpc.UpdateDocumentRequest, ...callopt.Option) (*knowledgerpc.DocumentDetail, error) {
	return nil, f.err
}

func (f *fakeKnowledgeClient) DeleteDocument(context.Context, *knowledgerpc.DocumentIDRequest, ...callopt.Option) (*knowledgerpc.Document, error) {
	return nil, f.err
}

func (f *fakeKnowledgeClient) SetDocumentStatus(context.Context, *knowledgerpc.SetDocumentStatusRequest, ...callopt.Option) (*knowledgerpc.Document, error) {
	return nil, f.err
}

func (f *fakeKnowledgeClient) ApplyDocumentOperation(context.Context, *knowledgerpc.ApplyDocumentOperationRequest, ...callopt.Option) (*knowledgerpc.DocumentOperationAck, error) {
	return nil, f.err
}

var _ knowledgeservice.Client = (*fakeKnowledgeClient)(nil)

type fakeCache struct {
	mu     sync.Mutex
	values map[string]int64
	err    error
}

func (f *fakeCache) Get(context.Context, string) ([]byte, error) {
	return nil, cache.ErrNotFound
}

func (f *fakeCache) Set(context.Context, string, []byte, time.Duration) error { return f.err }

func (f *fakeCache) SetIfAbsent(context.Context, string, []byte, time.Duration) (bool, error) {
	return true, f.err
}

func (f *fakeCache) Delete(context.Context, ...string) error { return f.err }

func (f *fakeCache) Increment(_ context.Context, key string, delta int64, _ time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	if f.values == nil {
		f.values = make(map[string]int64)
	}
	f.values[key] += delta
	return f.values[key], nil
}

func (f *fakeCache) Ping(context.Context) error { return f.err }
func (f *fakeCache) Close() error               { return f.err }

func (f *fakeCache) incrementCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := int64(0)
	for _, value := range f.values {
		count += value
	}
	return int(count)
}

var _ cache.KVStore = (*fakeCache)(nil)

func assertResponseMetadata(t *testing.T, response *ut.ResponseRecorder) {
	t.Helper()
	var envelope struct {
		RequestID string `json:"request_id"`
		TraceID   string `json:"trace_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response metadata: %v; body = %s", err, response.Body.String())
	}
	if envelope.RequestID == "" || envelope.RequestID != string(response.Header().Peek("X-Request-ID")) {
		t.Fatalf("request ID body = %q, header = %q", envelope.RequestID, response.Header().Peek("X-Request-ID"))
	}
	decoded, err := hex.DecodeString(envelope.TraceID)
	if err != nil || len(decoded) != 16 || strings.Trim(envelope.TraceID, "0") == "" ||
		envelope.TraceID != string(response.Header().Peek("X-Trace-ID")) {
		t.Fatalf("trace ID body = %q, header = %q", envelope.TraceID, response.Header().Peek("X-Trace-ID"))
	}
}

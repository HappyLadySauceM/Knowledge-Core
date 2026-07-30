package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/apperror"
	"github.com/HappyLadySauce/Knowledge-Core/internal/cache"
	identityrpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	knowledgerpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

func TestHTTPStatusMapping(t *testing.T) {
	tests := []struct {
		kind apperror.Kind
		want int
	}{
		{apperror.KindInvalidArgument, 400},
		{apperror.KindUnauthenticated, 401},
		{apperror.KindPermissionDenied, 403},
		{apperror.KindNotFound, 404},
		{apperror.KindConflict, 409},
		{apperror.KindRateLimited, 429},
		{apperror.KindDeadlineExceeded, 504},
		{apperror.KindUnavailable, 503},
		{apperror.KindInternal, 500},
	}
	for _, test := range tests {
		if got := HTTPStatus(test.kind); got != test.want {
			t.Errorf("HTTPStatus(%q) = %d, want %d", test.kind, got, test.want)
		}
	}
}

func TestRPCErrorMappingPreservesDomainCodes(t *testing.T) {
	tests := []struct {
		name       string
		write      func(context.Context, *app.RequestContext, error)
		err        error
		wantStatus int
		wantCode   int32
		wantText   string
	}{
		{
			name: "identity conflict", write: WriteIdentityError,
			err:        kerrors.NewBizStatusError(identityrpc.CodeConflict, "unsafe upstream message"),
			wantStatus: 409, wantCode: identityrpc.CodeConflict, wantText: "account already exists",
		},
		{
			name: "knowledge not found", write: WriteKnowledgeError,
			err:        kerrors.NewBizStatusError(knowledgerpc.CodeNotFound, "unsafe upstream message"),
			wantStatus: 404, wantCode: knowledgerpc.CodeNotFound, wantText: "document not found",
		},
		{
			name: "timeout", write: WriteKnowledgeError, err: context.DeadlineExceeded,
			wantStatus: 504, wantCode: 10011, wantText: "upstream request timed out",
		},
		{
			name: "unavailable", write: WriteIdentityError, err: errors.New("dial secret-service: password"),
			wantStatus: 503, wantCode: 10007, wantText: "service unavailable",
		},
		{
			name: "unknown business code", write: WriteIdentityError,
			err:        kerrors.NewBizStatusError(29998, "unsafe upstream message"),
			wantStatus: 502, wantCode: 10012, wantText: "invalid upstream response",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := ut.CreateUtRequestContext(http.MethodGet, "/", nil)
			test.write(context.Background(), request, test.err)
			if request.Response.StatusCode() != test.wantStatus {
				t.Fatalf("status = %d, want %d", request.Response.StatusCode(), test.wantStatus)
			}
			envelope := decodeErrorEnvelope(t, request.Response.Body())
			if envelope.Code != test.wantCode || envelope.Message != test.wantText {
				t.Fatalf("envelope = %#v", envelope)
			}
			if strings.Contains(string(request.Response.Body()), "unsafe") || strings.Contains(string(request.Response.Body()), "password") {
				t.Fatalf("response leaked upstream detail: %s", request.Response.Body())
			}
			assertContextMetadata(t, request, envelope)
		})
	}
}

func TestRecoveryNoRouteAndNoMethodUseStructuredResponses(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	engine := server.New(server.WithHandleMethodNotAllowed(true), server.WithRedirectTrailingSlash(false))
	engine.Use(RequestID())
	engine.Use(Trace(noopTracing)...)
	engine.Use(AccessLog(logger), JSONRecovery(logger), SecurityHeaders(), CORS(CORSConfig{}, nil))
	engine.GET("/panic", func(context.Context, *app.RequestContext) { panic("handler failed") })
	engine.GET("/ok", func(_ context.Context, request *app.RequestContext) { request.String(200, "ok") })
	engine.NoRoute(NoRoute())
	engine.NoMethod(NoMethod())

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCode   int32
	}{
		{"panic", http.MethodGet, "/panic", 500, 10999},
		{"no route", http.MethodGet, "/missing", 404, 10008},
		{"trailing slash", http.MethodGet, "/ok/", 404, 10008},
		{"no method", http.MethodPost, "/ok", 405, 10009},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := ut.PerformRequest(engine.Engine, test.method, test.path, nil,
				ut.Header{Key: "X-Trace-ID", Value: strings.Repeat("a", 32)})
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			envelope := decodeErrorEnvelope(t, response.Body.Bytes())
			if envelope.Code != test.wantCode {
				t.Fatalf("code = %d, want %d", envelope.Code, test.wantCode)
			}
			assertRecorderMetadata(t, response, envelope)
			if got := string(response.Header().Peek("X-Trace-ID")); got == strings.Repeat("a", 32) {
				t.Fatal("trusted inbound X-Trace-ID")
			}
			assertSecurityHeaders(t, response)
		})
	}
	if count := strings.Count(logs.String(), `"event":"panic_recovery"`); count != 1 {
		t.Fatalf("panic recovery log count = %d; logs = %s", count, logs.String())
	}
	if strings.Contains(logs.String(), "handler failed") {
		t.Fatalf("panic recovery log leaked the recovered value: %s", logs.String())
	}
}

func TestEnginePanicHandlerCatchesOuterMiddlewarePanic(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	engine := server.New()
	engine.PanicHandler = JSONPanicHandler(logger)
	engine.Use(
		RequestID(),
		func(context.Context, *app.RequestContext) { panic("tracing middleware failed") },
		JSONRecovery(logger),
		SecurityHeaders(),
	)
	engine.GET("/", func(_ context.Context, request *app.RequestContext) { request.Status(200) })

	response := ut.PerformRequest(engine.Engine, http.MethodGet, "/", nil)
	if response.Code != 500 {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	envelope := decodeErrorEnvelope(t, response.Body.Bytes())
	if envelope.Code != 10999 {
		t.Fatalf("envelope = %#v", envelope)
	}
	assertRecorderMetadata(t, response, envelope)
	assertSecurityHeaders(t, response)
	if count := strings.Count(logs.String(), `"event":"panic_recovery"`); count != 1 {
		t.Fatalf("panic recovery log count = %d; logs = %s", count, logs.String())
	}
	if !strings.Contains(logs.String(), `"panic_origin":"outer_middleware"`) {
		t.Fatalf("outer middleware origin missing: %s", logs.String())
	}
}

func TestCORSAllowsConfiguredOriginAndRejectsOthers(t *testing.T) {
	called := 0
	engine := server.New()
	engine.Use(RequestID())
	engine.Use(Trace(noopTracing)...)
	engine.Use(SecurityHeaders(), CORS(CORSConfig{AllowedOrigins: []string{"https://ui.example.test"}}, nil))
	engine.POST("/resource", func(_ context.Context, request *app.RequestContext) {
		called++
		request.Status(200)
	})

	preflight := ut.PerformRequest(engine.Engine, http.MethodOptions, "/resource", nil,
		ut.Header{Key: "Origin", Value: "https://ui.example.test"},
		ut.Header{Key: "Access-Control-Request-Method", Value: "POST"},
		ut.Header{Key: "Access-Control-Request-Headers", Value: "Authorization, traceparent"},
	)
	if preflight.Code != http.StatusNoContent || called != 0 {
		t.Fatalf("preflight = %d %s, handler calls = %d", preflight.Code, preflight.Body.String(), called)
	}
	if got := string(preflight.Header().Peek("Access-Control-Allow-Origin")); got != "https://ui.example.test" {
		t.Fatalf("allow origin = %q", got)
	}
	if len(preflight.Header().Peek("Access-Control-Allow-Credentials")) != 0 {
		t.Fatal("credentials were enabled")
	}

	denied := ut.PerformRequest(engine.Engine, http.MethodPost, "/resource", nil,
		ut.Header{Key: "Origin", Value: "https://attacker.example"})
	if denied.Code != http.StatusForbidden || called != 0 {
		t.Fatalf("denied = %d %s, handler calls = %d", denied.Code, denied.Body.String(), called)
	}
	if envelope := decodeErrorEnvelope(t, denied.Body.Bytes()); envelope.Code != 10005 {
		t.Fatalf("denied envelope = %#v", envelope)
	}
}

func TestEmptyCORSConfigAllowsOnlySameOrigin(t *testing.T) {
	engine := server.New()
	engine.Use(CORS(CORSConfig{}, nil))
	engine.POST("/resource", func(_ context.Context, request *app.RequestContext) { request.Status(200) })

	sameOrigin := ut.PerformRequest(engine.Engine, http.MethodPost, "http://api.example.test/resource", nil,
		ut.Header{Key: "Origin", Value: "http://api.example.test"})
	if sameOrigin.Code != 200 {
		t.Fatalf("same-origin response = %d %s", sameOrigin.Code, sameOrigin.Body.String())
	}
	crossOrigin := ut.PerformRequest(engine.Engine, http.MethodPost, "http://api.example.test/resource", nil,
		ut.Header{Key: "Origin", Value: "https://ui.example.test"})
	if crossOrigin.Code != 403 {
		t.Fatalf("cross-origin response = %d %s", crossOrigin.Code, crossOrigin.Body.String())
	}

	_, trustedProxy, err := net.ParseCIDR("0.0.0.0/0")
	if err != nil {
		t.Fatal(err)
	}
	proxied := server.New()
	proxied.Use(func(ctx context.Context, request *app.RequestContext) {
		request.Request.SetHost("api.example.test")
		request.Next(ctx)
	}, CORS(CORSConfig{}, []*net.IPNet{trustedProxy}))
	proxied.POST("/resource", func(_ context.Context, request *app.RequestContext) { request.Status(200) })
	proxiedHTTPS := ut.PerformRequest(proxied.Engine, http.MethodPost, "/resource", nil,
		ut.Header{Key: "Origin", Value: "https://api.example.test"},
		ut.Header{Key: "X-Forwarded-Proto", Value: "https"})
	if proxiedHTTPS.Code != 200 {
		t.Fatalf("trusted HTTPS proxy response = %d %s", proxiedHTTPS.Code, proxiedHTTPS.Body.String())
	}
	ambiguousForwardedProto := ut.PerformRequest(proxied.Engine, http.MethodPost, "/resource", nil,
		ut.Header{Key: "Origin", Value: "https://api.example.test"},
		ut.Header{Key: "X-Forwarded-Proto", Value: "https, http"})
	if ambiguousForwardedProto.Code != http.StatusForbidden {
		t.Fatalf("ambiguous forwarded proto response = %d %s", ambiguousForwardedProto.Code, ambiguousForwardedProto.Body.String())
	}

	untrusted := server.New()
	untrusted.Use(func(ctx context.Context, request *app.RequestContext) {
		request.Request.SetHost("api.example.test")
		request.Next(ctx)
	}, CORS(CORSConfig{}, nil))
	untrusted.POST("/resource", func(_ context.Context, request *app.RequestContext) { request.Status(200) })
	spoofedHTTPS := ut.PerformRequest(untrusted.Engine, http.MethodPost, "/resource", nil,
		ut.Header{Key: "Origin", Value: "https://api.example.test"},
		ut.Header{Key: "X-Forwarded-Proto", Value: "https"})
	if spoofedHTTPS.Code != 403 {
		t.Fatalf("untrusted forwarded proto response = %d %s", spoofedHTTPS.Code, spoofedHTTPS.Body.String())
	}
}

func TestRateLimitFixedWindowAndFailureBehavior(t *testing.T) {
	fixedNow := time.Date(2026, 7, 30, 12, 0, 30, 0, time.UTC)
	newEngine := func(store *recordingStore, global, auth int64) *server.Hertz {
		engine := server.New()
		engine.SetClientIPFunc(app.ClientIPWithOption(app.ClientIPOptions{}))
		engine.Use(RequestID())
		engine.Use(Trace(noopTracing)...)
		engine.Use(RateLimit(store, RateLimitConfig{
			Window: time.Minute, GlobalLimit: global, AuthLimit: auth, now: func() time.Time { return fixedNow },
		}))
		engine.GET("/health/live", func(_ context.Context, request *app.RequestContext) { request.Status(200) })
		engine.GET("/health/ready", func(_ context.Context, request *app.RequestContext) { request.Status(200) })
		engine.GET("/resource", func(_ context.Context, request *app.RequestContext) { request.Status(200) })
		engine.POST("/api/v1/auth/login", func(_ context.Context, request *app.RequestContext) { request.Status(200) })
		return engine
	}

	globalStore := &recordingStore{}
	global := newEngine(globalStore, 2, 1)
	for attempt := 1; attempt <= 3; attempt++ {
		response := ut.PerformRequest(global.Engine, http.MethodGet, "/resource", nil)
		if attempt < 3 && response.Code != 200 {
			t.Fatalf("global attempt %d status = %d", attempt, response.Code)
		}
		if attempt == 3 {
			if response.Code != 429 || string(response.Header().Peek("Retry-After")) != "30" {
				t.Fatalf("limited response = %d, retry-after = %q", response.Code, response.Header().Peek("Retry-After"))
			}
			if envelope := decodeErrorEnvelope(t, response.Body.Bytes()); envelope.Code != 10010 {
				t.Fatalf("limited envelope = %#v", envelope)
			}
		}
	}
	if globalStore.lastTTL != 30*time.Second {
		t.Fatalf("fixed-window TTL = %s", globalStore.lastTTL)
	}

	auth := newEngine(&recordingStore{}, 10, 1)
	if response := ut.PerformRequest(auth.Engine, http.MethodPost, "/api/v1/auth/login", nil); response.Code != 200 {
		t.Fatalf("first auth response = %d", response.Code)
	}
	if response := ut.PerformRequest(auth.Engine, http.MethodPost, "/api/v1/auth/login", nil); response.Code != 429 {
		t.Fatalf("second auth response = %d %s", response.Code, response.Body.String())
	}

	failing := newEngine(&recordingStore{err: errors.New("redis unavailable")}, 10, 10)
	response := ut.PerformRequest(failing.Engine, http.MethodGet, "/resource", nil)
	if response.Code != 503 || decodeErrorEnvelope(t, response.Body.Bytes()).Code != 10007 {
		t.Fatalf("cache failure response = %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{"/health/live", "/health/ready"} {
		if response := ut.PerformRequest(failing.Engine, http.MethodGet, path, nil); response.Code != 200 {
			t.Fatalf("health probe %s must bypass unavailable rate-limit storage: %d %s", path, response.Code, response.Body.String())
		}
	}

	finalSubmillisecondStore := &recordingStore{}
	request := ut.CreateUtRequestContext(http.MethodGet, "/resource", nil)
	nearBoundary := time.Date(2026, 7, 30, 12, 0, 59, 999_999_500, time.UTC)
	if !consumeRateLimit(context.Background(), request, finalSubmillisecondStore, "global", "127.0.0.1", nearBoundary, time.Minute, 10) {
		t.Fatal("near-boundary request was rejected")
	}
	if finalSubmillisecondStore.lastTTL != time.Millisecond {
		t.Fatalf("near-boundary TTL = %s", finalSubmillisecondStore.lastTTL)
	}
}

func TestTrustedProxyCIDRsControlForwardedClientIP(t *testing.T) {
	fixedNow := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	test := func(t *testing.T, trusted []*net.IPNet, wantSecondStatus int) {
		t.Helper()
		store := &recordingStore{}
		engine := server.New()
		clientIP := app.ClientIPWithOption(app.ClientIPOptions{
			RemoteIPHeaders: []string{"X-Forwarded-For"}, TrustedCIDRs: trusted,
		})
		engine.Use(func(ctx context.Context, request *app.RequestContext) {
			request.SetClientIPFunc(clientIP)
			request.Next(ctx)
		})
		engine.Use(RateLimit(store, RateLimitConfig{
			Window: time.Minute, GlobalLimit: 1, AuthLimit: 1, now: func() time.Time { return fixedNow },
		}))
		engine.GET("/", func(_ context.Context, request *app.RequestContext) { request.Status(200) })
		first := ut.PerformRequest(engine.Engine, http.MethodGet, "/", nil, ut.Header{Key: "X-Forwarded-For", Value: "198.51.100.10"})
		second := ut.PerformRequest(engine.Engine, http.MethodGet, "/", nil, ut.Header{Key: "X-Forwarded-For", Value: "198.51.100.11"})
		if first.Code != 200 || second.Code != wantSecondStatus {
			t.Fatalf("statuses = %d, %d; keys = %#v", first.Code, second.Code, store.counts)
		}
	}

	t.Run("empty trusts no forwarding headers", func(t *testing.T) { test(t, []*net.IPNet{}, 429) })
	_, allIPv4, err := net.ParseCIDR("0.0.0.0/0")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("trusted proxy uses forwarding header", func(t *testing.T) { test(t, []*net.IPNet{allIPv4}, 200) })
}

func TestLoadConfigValidatesSecuritySettings(t *testing.T) {
	values := map[string]string{
		"KC_GATEWAY_TRUSTED_PROXY_CIDRS":  "10.0.0.0/8, 2001:db8::/32",
		"KC_GATEWAY_CORS_ALLOWED_ORIGINS": "https://ui.example.test,http://localhost:3000",
		"KC_GATEWAY_RATE_LIMIT_WINDOW":    "30s",
		"KC_GATEWAY_RATE_LIMIT_GLOBAL":    "12",
		"KC_GATEWAY_RATE_LIMIT_AUTH":      "3",
	}
	config, err := LoadConfig(func(key string) (string, bool) {
		value, exists := values[key]
		return value, exists
	})
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(config.TrustedProxyCIDRs) != 2 || len(config.CORS.AllowedOrigins) != 2 ||
		config.RateLimit.Window != 30*time.Second || config.RateLimit.GlobalLimit != 12 || config.RateLimit.AuthLimit != 3 {
		t.Fatalf("config = %#v", config)
	}

	invalid := []struct {
		key   string
		value string
	}{
		{"KC_GATEWAY_TRUSTED_PROXY_CIDRS", "not-a-cidr"},
		{"KC_GATEWAY_CORS_ALLOWED_ORIGINS", "*"},
		{"KC_GATEWAY_CORS_ALLOWED_ORIGINS", "https://example.test/path"},
		{"KC_GATEWAY_CORS_ALLOWED_ORIGINS", "https://:443"},
		{"KC_GATEWAY_RATE_LIMIT_WINDOW", "0s"},
		{"KC_GATEWAY_RATE_LIMIT_GLOBAL", "0"},
		{"KC_GATEWAY_RATE_LIMIT_AUTH", "invalid"},
	}
	for _, test := range invalid {
		t.Run(test.key+"="+test.value, func(t *testing.T) {
			_, err := LoadConfig(func(key string) (string, bool) {
				if key == test.key {
					return test.value, true
				}
				return "", false
			})
			if err == nil {
				t.Fatal("LoadConfig() succeeded")
			}
		})
	}
	if err := (Config{
		CORS: CORSConfig{AllowedOrigins: []string{"*"}},
		RateLimit: RateLimitConfig{
			Window: time.Minute, GlobalLimit: 1, AuthLimit: 1,
		},
	}).Validate(); err == nil {
		t.Fatal("Config.Validate() accepted a wildcard origin")
	}
}

type decodedErrorEnvelope struct {
	Code      int32  `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	TraceID   string `json:"trace_id"`
}

func decodeErrorEnvelope(t *testing.T, body []byte) decodedErrorEnvelope {
	t.Helper()
	var envelope decodedErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode response %q: %v", body, err)
	}
	return envelope
}

func assertContextMetadata(t *testing.T, request *app.RequestContext, envelope decodedErrorEnvelope) {
	t.Helper()
	requestID := string(request.Response.Header.Peek("X-Request-ID"))
	traceID := string(request.Response.Header.Peek("X-Trace-ID"))
	if envelope.RequestID == "" || envelope.RequestID != requestID {
		t.Fatalf("request metadata = body %q, header %q", envelope.RequestID, requestID)
	}
	if !validTraceID(envelope.TraceID) || envelope.TraceID != traceID {
		t.Fatalf("trace metadata = body %q, header %q", envelope.TraceID, traceID)
	}
}

func assertRecorderMetadata(t *testing.T, response *ut.ResponseRecorder, envelope decodedErrorEnvelope) {
	t.Helper()
	if envelope.RequestID == "" || envelope.RequestID != string(response.Header().Peek("X-Request-ID")) {
		t.Fatalf("request metadata = body %q, header %q", envelope.RequestID, response.Header().Peek("X-Request-ID"))
	}
	if !validTraceID(envelope.TraceID) || envelope.TraceID != string(response.Header().Peek("X-Trace-ID")) {
		t.Fatalf("trace metadata = body %q, header %q", envelope.TraceID, response.Header().Peek("X-Trace-ID"))
	}
}

func assertSecurityHeaders(t *testing.T, response *ut.ResponseRecorder) {
	t.Helper()
	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
	}
	for header, expected := range want {
		if got := string(response.Header().Peek(header)); got != expected {
			t.Errorf("%s = %q, want %q", header, got, expected)
		}
	}
}

func noopTracing(ctx context.Context, request *app.RequestContext) {
	request.Next(ctx)
}

type recordingStore struct {
	mu      sync.Mutex
	counts  map[string]int64
	err     error
	lastTTL time.Duration
}

func (*recordingStore) Get(context.Context, string) ([]byte, error)                { return nil, cache.ErrNotFound }
func (s *recordingStore) Set(context.Context, string, []byte, time.Duration) error { return s.err }
func (s *recordingStore) SetIfAbsent(context.Context, string, []byte, time.Duration) (bool, error) {
	return true, s.err
}
func (s *recordingStore) Delete(context.Context, ...string) error { return s.err }
func (s *recordingStore) Ping(context.Context) error              { return s.err }
func (s *recordingStore) Close() error                            { return s.err }

func (s *recordingStore) Increment(_ context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 0, s.err
	}
	if s.counts == nil {
		s.counts = make(map[string]int64)
	}
	s.counts[key] += delta
	s.lastTTL = ttl
	return s.counts[key], nil
}

var _ cache.KVStore = (*recordingStore)(nil)

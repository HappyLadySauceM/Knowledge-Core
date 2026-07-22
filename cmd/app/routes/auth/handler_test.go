package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/HappyLadySauce/Knowledge-Core/cmd/app/svc"
	v1 "github.com/HappyLadySauce/Knowledge-Core/cmd/app/types/v1"
	internalauth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	"github.com/HappyLadySauce/Knowledge-Core/internal/config"
	apperrors "github.com/HappyLadySauce/Knowledge-Core/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/internal/options"
	"github.com/HappyLadySauce/Knowledge-Core/internal/testutil"
	internaluser "github.com/HappyLadySauce/Knowledge-Core/internal/user"
)

func TestRegisterLoginAndRefreshReturnTokenEnvelope(t *testing.T) {
	harness := newAuthHarness(t)

	register := harness.request(t, http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "Alice",
		"password": "StrongPass_123",
		"email":    "alice@example.com",
		"role":     internaluser.RoleAdmin,
	}, "")
	registered := decodeEnvelopeData[v1.TokenResponse](t, register, http.StatusCreated, apperrors.MessageOK)
	if registered.User.Role != internaluser.RoleUser || registered.User.Status != internaluser.StatusActive {
		t.Fatalf("registered user should be active normal user: %+v", registered.User)
	}
	if registered.User.Avatar != "" || registered.User.Bio != "" {
		t.Fatalf("new user should have empty profile fields: %+v", registered.User)
	}

	login := harness.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "alice",
		"password": "StrongPass_123",
	}, "")
	loggedIn := decodeEnvelopeData[v1.TokenResponse](t, login, http.StatusOK, apperrors.MessageOK)
	if loggedIn.AccessToken == "" || loggedIn.RefreshToken == "" || loggedIn.TokenType != internalauth.TokenTypeBearer {
		t.Fatalf("login should return OAuth2 token response in data: %+v", loggedIn)
	}
	if active := harness.countActiveRefreshTokens(t); active == 0 {
		t.Fatalf("expected refresh token audit rows after login")
	}
	if keys := harness.countRedisRefreshTokenKeys(t); keys == 0 {
		t.Fatalf("expected redis refresh token keys after login")
	}

	harness.deleteRedisRefreshTokenKeys(t)

	refresh := harness.request(t, http.MethodPost, "/api/v1/auth/refresh", map[string]any{
		"refresh_token": loggedIn.RefreshToken,
	}, "")
	rotated := decodeEnvelopeData[v1.TokenResponse](t, refresh, http.StatusOK, apperrors.MessageOK)
	if rotated.RefreshToken == "" || rotated.RefreshToken == loggedIn.RefreshToken {
		t.Fatalf("refresh token was not rotated")
	}
	if keys := harness.countRedisRefreshTokenKeys(t); keys == 0 {
		t.Fatalf("expected refresh fallback to repopulate redis")
	}

	oldRefresh := harness.request(t, http.MethodPost, "/api/v1/auth/refresh", map[string]any{
		"refresh_token": loggedIn.RefreshToken,
	}, "")
	decodeEnvelopeData[any](t, oldRefresh, http.StatusUnauthorized, apperrors.MessageUnauthorized)
}

func TestDefaultAdminCanLogin(t *testing.T) {
	harness := newAuthHarness(t)

	response := harness.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "admin",
		"password": "ChangeMe_123456!",
	}, "")
	token := decodeEnvelopeData[v1.TokenResponse](t, response, http.StatusOK, apperrors.MessageOK)
	if token.User.Role != internaluser.RoleAdmin || token.Scope != "role:admin" {
		t.Fatalf("admin login returned wrong user/scope: %+v", token)
	}
	if !token.User.MustChangePassword {
		t.Fatalf("bootstrap admin should require password change on first login")
	}
}

func TestLogoutRevokesRefreshToken(t *testing.T) {
	harness := newAuthHarness(t)
	token := harness.registerUser(t, "logout-user")

	logout := harness.request(t, http.MethodPost, "/api/v1/auth/logout", map[string]any{
		"refresh_token": token.RefreshToken,
	}, token.AccessToken)
	decodeEnvelopeData[any](t, logout, http.StatusOK, apperrors.MessageOK)

	refresh := harness.request(t, http.MethodPost, "/api/v1/auth/refresh", map[string]any{
		"refresh_token": token.RefreshToken,
	}, "")
	decodeEnvelopeData[any](t, refresh, http.StatusUnauthorized, apperrors.MessageUnauthorized)
}

func TestRefreshRejectsRevokedTokenEvenWhenRedisKeyExists(t *testing.T) {
	harness := newAuthHarness(t)
	token := harness.registerUser(t, "revoked-redis-user")
	if keys := harness.countRedisRefreshTokenKeys(t); keys == 0 {
		t.Fatalf("expected redis refresh token key before sql revoke")
	}

	if _, err := harness.db.ExecContext(context.Background(), `
UPDATE refresh_tokens
SET revoked_at = $1, revoked_reason = 'test_sql_revoke'
WHERE user_id = $2 AND revoked_at IS NULL`, time.Now().UTC(), token.User.ID); err != nil {
		t.Fatalf("sql revoke refresh token failed: %v", err)
	}
	refresh := harness.request(t, http.MethodPost, "/api/v1/auth/refresh", map[string]any{
		"refresh_token": token.RefreshToken,
	}, "")
	decodeEnvelopeData[any](t, refresh, http.StatusUnauthorized, apperrors.MessageUnauthorized)
}

func TestRefreshUsesPostgresFallbackWhenRedisUnavailable(t *testing.T) {
	harness := newAuthHarnessWithoutRedis(t)
	token := harness.registerUser(t, "fallback-user")

	refresh := harness.request(t, http.MethodPost, "/api/v1/auth/refresh", map[string]any{
		"refresh_token": token.RefreshToken,
	}, "")
	rotated := decodeEnvelopeData[v1.TokenResponse](t, refresh, http.StatusOK, apperrors.MessageOK)
	if rotated.RefreshToken == "" || rotated.RefreshToken == token.RefreshToken {
		t.Fatalf("refresh token was not rotated with postgres fallback")
	}
}

func TestBadAuthRequestsReturnEnvelope(t *testing.T) {
	harness := newAuthHarness(t)

	badRequest := harness.request(t, http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "missing-password",
	}, "")
	decodeEnvelopeData[any](t, badRequest, http.StatusBadRequest, apperrors.MessageInvalidRequest)

	_ = harness.registerUser(t, "duplicate-user")
	conflict := harness.request(t, http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "duplicate-user",
		"password": "StrongPass_123",
	}, "")
	decodeEnvelopeData[any](t, conflict, http.StatusConflict, apperrors.MessageConflict)

	wrongPassword := harness.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "duplicate-user",
		"password": "wrong-password",
	}, "")
	decodeEnvelopeData[any](t, wrongPassword, http.StatusUnauthorized, apperrors.MessageUnauthorized)

	badLogout := harness.request(t, http.MethodPost, "/api/v1/auth/logout", map[string]any{}, "")
	decodeEnvelopeData[any](t, badLogout, http.StatusUnauthorized, apperrors.MessageUnauthorized)

	invalidLogout := harness.request(t, http.MethodPost, "/api/v1/auth/logout", map[string]any{
		"refresh_token": "invalid-refresh-token",
	}, "invalid-access-token")
	decodeEnvelopeData[any](t, invalidLogout, http.StatusUnauthorized, apperrors.MessageUnauthorized)
}

func TestAuthMeRouteIsRemoved(t *testing.T) {
	harness := newAuthHarness(t)

	response := harness.request(t, http.MethodGet, "/api/v1/auth/me", nil, "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("GET /api/v1/auth/me status = %d, want 404", response.Code)
	}
}

func TestJWTSecretValidationRejectsShortSecretAndGeneratesFallback(t *testing.T) {
	short := options.NewJWTOptions()
	short.Secret = "short"
	short.AccessTTL = time.Minute
	short.RefreshTTL = time.Hour
	if err := short.Validate(); err == nil {
		t.Fatalf("expected short jwt secret to fail validation")
	}

	for _, secret := range []string{"", "Knowledge-Core-dev-secret-change-me-32bytes"} {
		opts := options.NewJWTOptions()
		opts.Secret = secret
		opts.AccessTTL = time.Minute
		opts.RefreshTTL = time.Hour
		if err := opts.Validate(); err != nil {
			t.Fatalf("expected jwt secret %q to generate fallback, got %v", secret, err)
		}
		if len(opts.Secret) < 32 || opts.Secret == secret {
			t.Fatalf("jwt secret fallback was not generated for %q", secret)
		}
	}
}

type authHarness struct {
	router      *gin.Engine
	db          *sql.DB
	redisPrefix string
	redisClient *redis.Client
}

func newAuthHarness(t *testing.T) *authHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, jwtOptions := newTestDB(t)
	redisClient, redisPrefix := testutil.NewCacheClient(t)
	authSvc := internalauth.NewService(db, jwtOptions, redisClient, internalauth.ServiceOptions{KeyPrefix: redisPrefix})
	sc := &svc.ServiceContext{
		Config: &config.Config{JWT: jwtOptions},
		DB:     db,
		Redis:  redisClient,
		Auth:   authSvc,
	}
	// Bootstrap admin so TestDefaultAdminCanLogin can verify the default admin.
	// 引导创建 admin 用户，使 TestDefaultAdminCanLogin 可验证默认管理员。
	t.Setenv("KNOWLEDGE_CORE_ADMIN_PASSWORD", "ChangeMe_123456!")
	if err := authSvc.EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("bootstrap admin failed: %v", err)
	}
	router := gin.New()
	RegisterRoutes(router.Group("/api/v1"), authSvc, sc)
	return &authHarness{router: router, db: db, redisPrefix: redisPrefix, redisClient: redisClient}
}

func newAuthHarnessWithoutRedis(t *testing.T) *authHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, jwtOptions := newTestDB(t)
	authSvc := internalauth.NewService(db, jwtOptions, nil, internalauth.ServiceOptions{KeyPrefix: "knowledge-core-test-no-redis"})
	sc := &svc.ServiceContext{
		Config: &config.Config{JWT: jwtOptions},
		DB:     db,
		Auth:   authSvc,
	}
	t.Setenv("KNOWLEDGE_CORE_ADMIN_PASSWORD", "ChangeMe_123456!")
	if err := authSvc.EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("bootstrap admin failed: %v", err)
	}
	router := gin.New()
	RegisterRoutes(router.Group("/api/v1"), authSvc, sc)
	return &authHarness{router: router, db: db}
}

func (h *authHarness) registerUser(t *testing.T, username string) v1.TokenResponse {
	t.Helper()
	response := h.request(t, http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": username,
		"password": "StrongPass_123",
	}, "")
	return decodeEnvelopeData[v1.TokenResponse](t, response, http.StatusCreated, apperrors.MessageOK)
}

func (h *authHarness) request(t *testing.T, method, path string, body any, accessToken string) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request failed: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	response := httptest.NewRecorder()
	h.router.ServeHTTP(response, req)
	return response
}

func (h *authHarness) countActiveRefreshTokens(t *testing.T) int {
	t.Helper()
	var count int
	if err := h.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM refresh_tokens WHERE revoked_at IS NULL`).Scan(&count); err != nil {
		t.Fatalf("count refresh tokens failed: %v", err)
	}
	return count
}

func (h *authHarness) countRedisRefreshTokenKeys(t *testing.T) int {
	t.Helper()
	if h.redisClient == nil {
		return 0
	}
	return len(h.scanRedisRefreshTokenKeys(t))
}

func (h *authHarness) deleteRedisRefreshTokenKeys(t *testing.T) {
	t.Helper()
	keys := h.scanRedisRefreshTokenKeys(t)
	if len(keys) > 0 {
		if err := h.redisClient.Del(context.Background(), keys...).Err(); err != nil {
			t.Fatalf("delete redis refresh token keys failed: %v", err)
		}
	}
}

func (h *authHarness) scanRedisRefreshTokenKeys(t *testing.T) []string {
	t.Helper()
	if h.redisClient == nil {
		return nil
	}
	ctx := context.Background()
	pattern := h.redisPrefix + ":auth:refresh:token:*"
	var cursor uint64
	var keys []string
	for {
		batch, next, err := h.redisClient.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			t.Fatalf("scan redis refresh token keys failed: %v", err)
		}
		keys = append(keys, batch...)
		if next == 0 {
			return keys
		}
		cursor = next
	}
}

func newTestDB(t *testing.T) (*sql.DB, *options.JWTOptions) {
	t.Helper()
	db := testutil.NewDB(t)
	return db, &options.JWTOptions{
		Issuer:     "Knowledge-Core",
		Secret:     "Knowledge-Core-test-secret-32bytes",
		AccessTTL:  time.Minute,
		RefreshTTL: time.Hour,
	}
}

type responseEnvelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

func decodeEnvelopeData[T any](t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantMessage string) T {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d, body = %s", response.Code, wantStatus, response.Body.String())
	}
	var envelope responseEnvelope[T]
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response failed: %v; body = %s", err, response.Body.String())
	}
	if envelope.Code != wantStatus {
		t.Fatalf("envelope code = %d, want %d", envelope.Code, wantStatus)
	}
	if envelope.Message != wantMessage {
		t.Fatalf("envelope message = %q, want %q", envelope.Message, wantMessage)
	}
	return envelope.Data
}

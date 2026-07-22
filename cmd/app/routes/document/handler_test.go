package document

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	authroute "github.com/HappyLadySauce/Knowledge-Core/cmd/app/routes/auth"
	"github.com/HappyLadySauce/Knowledge-Core/cmd/app/svc"
	"github.com/HappyLadySauce/Knowledge-Core/cmd/app/types/common"
	v1 "github.com/HappyLadySauce/Knowledge-Core/cmd/app/types/v1"
	internalauth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	"github.com/HappyLadySauce/Knowledge-Core/internal/config"
	internaldocument "github.com/HappyLadySauce/Knowledge-Core/internal/document"
	apperrors "github.com/HappyLadySauce/Knowledge-Core/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/internal/options"
	"github.com/HappyLadySauce/Knowledge-Core/internal/taxonomy"
	"github.com/HappyLadySauce/Knowledge-Core/internal/testutil"
)

func TestDocumentHTTPPublishedVisibilityAndAdminAuth(t *testing.T) {
	harness := newDocumentHarness(t)
	adminToken := harness.loginAdmin(t)
	userToken := harness.registerUser(t, "doc-user")

	forbidden := harness.request(t, http.MethodGet, "/api/v1/admin/documents", nil, userToken.AccessToken)
	decodeEnvelopeData[any](t, forbidden, http.StatusForbidden, apperrors.MessageForbidden)

	create := harness.request(t, http.MethodPost, "/api/v1/admin/documents", map[string]any{
		"title":       "Draft Note",
		"summary":     "private",
		"content":     "draft body",
		"category_id": harness.categoryID,
		"tag_ids":     []int64{harness.tagID},
		"status":      internaldocument.StatusDraft,
	}, adminToken.AccessToken)
	created := decodeEnvelopeData[v1.DocumentResponse](t, create, http.StatusCreated, apperrors.MessageOK)

	publicList := harness.request(t, http.MethodGet, "/api/v1/documents", nil, "")
	list := decodeEnvelopeData[v1.ListDocumentsResponse](t, publicList, http.StatusOK, apperrors.MessageOK)
	if list.Total != 0 {
		t.Fatalf("public draft total = %d, want 0", list.Total)
	}

	publish := harness.request(t, http.MethodPatch, "/api/v1/admin/documents/"+itoa(created.ID), map[string]any{
		"status": internaldocument.StatusPublished,
	}, adminToken.AccessToken)
	published := decodeEnvelopeData[v1.DocumentResponse](t, publish, http.StatusOK, apperrors.MessageOK)
	if published.Status != internaldocument.StatusPublished {
		t.Fatalf("status = %s, want published", published.Status)
	}

	publicGet := harness.request(t, http.MethodGet, "/api/v1/documents/"+itoa(created.ID), nil, "")
	publicDoc := decodeEnvelopeData[v1.DocumentResponse](t, publicGet, http.StatusOK, apperrors.MessageOK)
	if publicDoc.Content != "draft body" || len(publicDoc.Tags) != 1 {
		t.Fatalf("unexpected public document: %+v", publicDoc)
	}
}

func TestDocumentCollabWebSocketSnapshotAckAndUserForbidden(t *testing.T) {
	harness := newDocumentHarness(t)
	adminToken := harness.loginAdmin(t)
	userToken := harness.registerUser(t, "collab-user")

	create := harness.request(t, http.MethodPost, "/api/v1/admin/documents", map[string]any{
		"title":   "Collab Note",
		"content": "initial body",
	}, adminToken.AccessToken)
	created := decodeEnvelopeData[v1.DocumentResponse](t, create, http.StatusCreated, apperrors.MessageOK)
	server := httptest.NewServer(harness.router)
	defer server.Close()

	forbiddenHeader := http.Header{}
	forbiddenHeader.Set("Authorization", "Bearer "+userToken.AccessToken)
	forbiddenHeader.Set("Origin", server.URL)
	forbiddenURL := websocketURL(server.URL, "/api/v1/admin/documents/"+itoa(created.ID)+"/collab")
	_, forbiddenResponse, err := websocket.DefaultDialer.Dial(forbiddenURL, forbiddenHeader)
	if err == nil {
		t.Fatalf("user websocket unexpectedly connected")
	}
	if forbiddenResponse == nil || forbiddenResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("forbidden websocket status = %v, want 403", forbiddenResponse)
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+adminToken.AccessToken)
	header.Set("Origin", server.URL)
	conn, response, err := websocket.DefaultDialer.Dial(forbiddenURL, header)
	if err != nil {
		if response != nil {
			t.Fatalf("admin websocket failed: %v status=%d", err, response.StatusCode)
		}
		t.Fatalf("admin websocket failed: %v", err)
	}
	defer conn.Close()

	snapshot := readCollabMessage(t, conn, "snapshot")
	if snapshot.Version != created.CurrentVersion {
		t.Fatalf("snapshot version = %d, want %d", snapshot.Version, created.CurrentVersion)
	}
	op := v1.DocumentOperationRequest{
		OpID:                 "ws-op-1",
		BaseDocumentVersion:  created.CurrentVersion,
		BlockID:              created.Blocks[0].BlockID,
		ExpectedBlockVersion: created.Blocks[0].Version,
		Type:                 internaldocument.OpTypeUpdateBlock,
		PayloadJSON:          `{"text_content":"updated over websocket"}`,
	}
	if err := conn.WriteJSON(collabMessage{Type: "op", Ops: []v1.DocumentOperationRequest{op}}); err != nil {
		t.Fatalf("write websocket op failed: %v", err)
	}
	ack := readCollabMessage(t, conn, "ack")
	if ack.Version != created.CurrentVersion+1 {
		t.Fatalf("ack version = %d, want %d", ack.Version, created.CurrentVersion+1)
	}
}

type documentHarness struct {
	router     *gin.Engine
	categoryID int64
	tagID      int64
}

func newDocumentHarness(t *testing.T) *documentHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db := newDocumentRouteTestDB(t)
	jwtOptions := &options.JWTOptions{
		Issuer:     "Knowledge-Core",
		Secret:     "Knowledge-Core-test-secret-32bytes",
		AccessTTL:  time.Minute,
		RefreshTTL: time.Hour,
	}
	redisClient, redisPrefix := testutil.NewCacheClient(t)
	authSvc := internalauth.NewService(db, jwtOptions, redisClient, internalauth.ServiceOptions{KeyPrefix: redisPrefix})
	sc := &svc.ServiceContext{
		Config: &config.Config{
			JWT:       jwtOptions,
			WebSocket: options.NewWebSocketOptions(),
		},
		DB:    db,
		Redis: redisClient,
		Auth:  authSvc,
	}
	taxonomies := taxonomy.NewService(db)
	category, err := taxonomies.CreateCategory(context.Background(), taxonomy.CategoryCommand{Name: "Tech", Slug: "tech"})
	if err != nil {
		t.Fatalf("create category failed: %v", err)
	}
	tag, err := taxonomies.CreateTag(context.Background(), taxonomy.TagCommand{Name: "Go", Slug: "go"})
	if err != nil {
		t.Fatalf("create tag failed: %v", err)
	}
	documentService, err := internaldocument.NewService(db)
	if err != nil {
		t.Fatalf("create document service failed: %v", err)
	}

	router := gin.New()
	group := router.Group("/api/v1")
	// Bootstrap admin so loginAdmin can authenticate.
	// 引导创建 admin 用户，使 loginAdmin 可认证。
	t.Setenv("KNOWLEDGE_CORE_ADMIN_PASSWORD", "ChangeMe_123456!")
	if err := authSvc.EnsureAdmin(context.Background()); err != nil {
		t.Fatalf("bootstrap admin failed: %v", err)
	}
	authroute.RegisterRoutes(group, authSvc, sc)
	RegisterRoutes(group, documentService, sc)
	return &documentHarness{router: router, categoryID: category.ID, tagID: tag.ID}
}

func (h *documentHarness) loginAdmin(t *testing.T) v1.TokenResponse {
	t.Helper()
	response := h.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "admin",
		"password": "ChangeMe_123456!",
	}, "")
	token := decodeEnvelopeData[v1.TokenResponse](t, response, http.StatusOK, apperrors.MessageOK)
	if !token.User.MustChangePassword {
		return token
	}
	change := h.request(t, http.MethodPut, "/api/v1/users/me/password", map[string]any{
		"old_password": "ChangeMe_123456!",
		"new_password": "AdminPass_123456!",
	}, token.AccessToken)
	decodeEnvelopeData[any](t, change, http.StatusOK, apperrors.MessageOK)
	login := h.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "admin",
		"password": "AdminPass_123456!",
	}, "")
	return decodeEnvelopeData[v1.TokenResponse](t, login, http.StatusOK, apperrors.MessageOK)
}

func (h *documentHarness) registerUser(t *testing.T, username string) v1.TokenResponse {
	t.Helper()
	response := h.request(t, http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": username,
		"password": "StrongPass_123",
	}, "")
	return decodeEnvelopeData[v1.TokenResponse](t, response, http.StatusCreated, apperrors.MessageOK)
}

func (h *documentHarness) request(t *testing.T, method, path string, body any, accessToken string) *httptest.ResponseRecorder {
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

func decodeEnvelopeData[T any](t *testing.T, response *httptest.ResponseRecorder, status int, message string) T {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, status, response.Body.String())
	}
	var envelope common.Response[T]
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope failed: %v; body=%s", err, response.Body.String())
	}
	if envelope.Message != message {
		t.Fatalf("message = %s, want %s", envelope.Message, message)
	}
	return envelope.Data
}

func newDocumentRouteTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return testutil.NewDB(t)
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}

func websocketURL(baseURL, path string) string {
	return strings.Replace(baseURL, "http://", "ws://", 1) + path
}

func readCollabMessage(t *testing.T, conn *websocket.Conn, wantType string) collabMessage {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			t.Fatalf("set websocket read deadline failed: %v", err)
		}
		var msg collabMessage
		if err := conn.ReadJSON(&msg); err != nil {
			continue
		}
		if msg.Type == wantType {
			return msg
		}
	}
	t.Fatalf("websocket message %q not received", wantType)
	return collabMessage{}
}

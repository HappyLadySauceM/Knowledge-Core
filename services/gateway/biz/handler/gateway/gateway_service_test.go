package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	identityv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	knowledgev1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge/knowledgeservice"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
	gatewaymodel "github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/model/gateway"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/config"
	gatewaymiddleware "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route/param"
	"github.com/cloudwego/kitex/client/callopt"
)

type identityStub struct {
	registered        *identityv1.User
	authentication    *identityv1.Authentication
	currentUser       *identityv1.User
	registerErr       error
	authenticateErr   error
	currentUserErr    error
	registerCalls     int
	authenticateCalls int
	currentUserCalls  int
}

func (s *identityStub) Ping(context.Context, *commonv1.PingRequest, ...callopt.Option) (*commonv1.PingResponse, error) {
	return nil, nil
}
func (s *identityStub) Register(context.Context, *identityv1.RegisterRequest, ...callopt.Option) (*identityv1.User, error) {
	s.registerCalls++
	return s.registered, s.registerErr
}
func (s *identityStub) Authenticate(context.Context, *identityv1.AuthenticateRequest, ...callopt.Option) (*identityv1.Authentication, error) {
	s.authenticateCalls++
	return s.authentication, s.authenticateErr
}
func (s *identityStub) GetCurrentUser(context.Context, *identityv1.CurrentUserRequest, ...callopt.Option) (*identityv1.User, error) {
	s.currentUserCalls++
	return s.currentUser, s.currentUserErr
}

func TestLoginReturnsBearerAccessToken(t *testing.T) {
	identity := &identityStub{authentication: &identityv1.Authentication{
		User: completeUser(), AccessToken: "signed-token", ExpiresAt: "2026-08-02T12:15:00Z",
	}}
	request := handlerRequest(identity, `{"identifier":"alice","password":"password"}`)
	Login(context.Background(), request)
	if request.Response.StatusCode() != consts.StatusOK {
		t.Fatalf("status = %d, body = %s", request.Response.StatusCode(), request.Response.Body())
	}
	var response gatewaymodel.SessionData
	if err := jsoncodec.Unmarshal(request.Response.Body(), &response); err != nil {
		t.Fatal(err)
	}
	if response.AccessToken != "signed-token" || response.TokenType != "Bearer" || response.User == nil {
		t.Fatalf("response = %#v", response)
	}
}

type verifierStub struct{}

func (verifierStub) Verify(string) (coreauth.Principal, error) {
	return coreauth.Principal{}, errors.New("invalid")
}

type limiterStub struct{}

func (limiterStub) Consume(context.Context, string, string, time.Time, time.Duration, int64) (bool, time.Duration, error) {
	return true, 0, nil
}

func TestRegisterUsesStrictJSONAndMapsUser(t *testing.T) {
	identity := &identityStub{registered: completeUser()}
	request := handlerRequest(identity, `{"username":"alice","email":"alice@example.com","password":"password","unknown":true}`)
	Register(context.Background(), request)
	if request.Response.StatusCode() != consts.StatusBadRequest || identity.registerCalls != 0 {
		t.Fatalf("strict request status = %d, calls = %d", request.Response.StatusCode(), identity.registerCalls)
	}

	request = handlerRequest(identity, `{"username":"alice","email":"alice@example.com","password":"password"}`)
	Register(context.Background(), request)
	if request.Response.StatusCode() != consts.StatusCreated {
		t.Fatalf("success status = %d, body = %s", request.Response.StatusCode(), request.Response.Body())
	}
	var response gatewaymodel.UserData
	if err := jsoncodec.Unmarshal(request.Response.Body(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != "7" || response.Username != "alice" || request.Response.Header.Get("X-Request-ID") == "" {
		t.Fatalf("response = %#v", response)
	}
}

func TestRegisterRejectsMissingOrEmptyRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing username", body: `{"email":"alice@example.com","password":"password"}`},
		{name: "missing email", body: `{"username":"alice","password":"password"}`},
		{name: "missing password", body: `{"username":"alice","email":"alice@example.com"}`},
		{name: "empty username", body: `{"username":"","email":"alice@example.com","password":"password"}`},
		{name: "empty email", body: `{"username":"alice","email":"","password":"password"}`},
		{name: "empty password", body: `{"username":"alice","email":"alice@example.com","password":""}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := &identityStub{}
			request := handlerRequest(identity, test.body)

			Register(context.Background(), request)

			if request.Response.StatusCode() != consts.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", request.Response.StatusCode(), request.Response.Body())
			}
			if identity.registerCalls != 0 {
				t.Fatalf("Register() calls = %d, want 0", identity.registerCalls)
			}
		})
	}
}

func TestLoginRejectsMissingOrEmptyRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing identifier", body: `{"password":"password"}`},
		{name: "missing password", body: `{"identifier":"alice"}`},
		{name: "empty identifier", body: `{"identifier":"","password":"password"}`},
		{name: "empty password", body: `{"identifier":"alice","password":""}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := &identityStub{}
			request := handlerRequest(identity, test.body)

			Login(context.Background(), request)

			if request.Response.StatusCode() != consts.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", request.Response.StatusCode(), request.Response.Body())
			}
			if identity.authenticateCalls != 0 {
				t.Fatalf("Authenticate() calls = %d, want 0", identity.authenticateCalls)
			}
		})
	}
}

func TestRegisterMapsIdentityBusinessError(t *testing.T) {
	definition := apperror.MustDefine(
		identityv1.CodeAccountLocked, "identity.account_locked", apperror.KindPermissionDenied, "account is locked",
	)
	identity := &identityStub{registerErr: apperror.ToKitexBizStatus(context.Background(), definition.New())}
	request := handlerRequest(identity, `{"username":"alice","email":"alice@example.com","password":"password"}`)
	Register(context.Background(), request)
	if request.Response.StatusCode() != 423 {
		t.Fatalf("status = %d, body = %s", request.Response.StatusCode(), request.Response.Body())
	}
}

func TestCreateDocumentUsesTrustedLocationAndStrongETag(t *testing.T) {
	var received *knowledgev1.CreateDocumentRequest
	knowledge := &knowledgeStub{createDocument: func(ctx context.Context, input *knowledgev1.CreateDocumentRequest) (*knowledgev1.Document, error) {
		received = input
		if token := coreauth.AccessToken(ctx); token != "signed-token" {
			t.Fatalf("access token = %q", token)
		}
		return completeDocument(), nil
	}}
	request := handlerRequest(&identityStub{}, `{"title":"Design","summary":"Current state"}`)
	request.Request.Header.Set("Idempotency-Key", "create-document-1")
	request.Set("gateway.access_token", "signed-token")
	dependencies, _ := gatewaymiddleware.FromRequest(request)
	dependencies.Knowledge = knowledge
	dependencies.Endpoints = config.EndpointOptions{PublicBaseURL: "https://api.example.com", CollaborationWebSocketURL: "wss://collaboration.example.com/collaboration"}

	CreateDocument(context.Background(), request)

	if request.Response.StatusCode() != consts.StatusCreated {
		t.Fatalf("status = %d, body = %s", request.Response.StatusCode(), request.Response.Body())
	}
	if received == nil || received.IdempotencyKey == nil || *received.IdempotencyKey != "create-document-1" {
		t.Fatalf("request = %#v", received)
	}
	if got := string(request.Response.Header.Peek("ETag")); got != `"1"` {
		t.Fatalf("ETag = %q", got)
	}
	if got := string(request.Response.Header.Peek("Location")); got != "https://api.example.com/api/v1/studio/documents/0198f0e0-7b6d-7a11-8e21-1123456789ab" {
		t.Fatalf("Location = %q", got)
	}
}

func TestGatewayRejectsAmbiguousDocumentInputs(t *testing.T) {
	t.Run("unknown query", func(t *testing.T) {
		request := app.NewContext(0)
		request.Request.URI().SetQueryString("unknown=value")
		ListPublishedDocuments(context.Background(), request)
		if request.Response.StatusCode() != consts.StatusBadRequest {
			t.Fatalf("status = %d", request.Response.StatusCode())
		}
	})

	t.Run("non-decimal user id", func(t *testing.T) {
		request := app.NewContext(1)
		request.Params = param.Params{
			{Key: "document_id", Value: "0198f0e0-7b6d-7a11-8e21-1123456789ab"},
			{Key: "user_id", Value: "+7"},
		}
		request.Request.Header.Set("If-Match", `"1"`)
		request.Request.Header.Set("Content-Type", "application/json")
		request.Request.SetBodyString(`{"role":"viewer"}`)
		UpdateMember(context.Background(), request)
		if request.Response.StatusCode() != consts.StatusBadRequest {
			t.Fatalf("status = %d", request.Response.StatusCode())
		}
	})

	t.Run("weak etag", func(t *testing.T) {
		request := app.NewContext(0)
		request.Request.Header.Set("If-Match", `W/"1"`)
		if _, err := expectedRevision(request); err == nil {
			t.Fatal("expectedRevision() accepted a weak ETag")
		}
	})
}

type knowledgeStub struct {
	knowledgeservice.Client
	createDocument func(context.Context, *knowledgev1.CreateDocumentRequest) (*knowledgev1.Document, error)
}

func (s *knowledgeStub) CreateDocument(ctx context.Context, input *knowledgev1.CreateDocumentRequest, _ ...callopt.Option) (*knowledgev1.Document, error) {
	return s.createDocument(ctx, input)
}

func handlerRequest(identity gatewaymiddleware.IdentityClient, body string) *app.RequestContext {
	request := app.NewContext(0)
	request.Request.SetBodyString(body)
	request.Request.Header.Set("Content-Type", "application/json")
	request.Set("gateway.dependencies", &gatewaymiddleware.Dependencies{
		Identity: identity, Verifier: verifierStub{}, Limiter: limiterStub{}, Health: health.NewRegistry(),
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		RateLimit: config.RateLimitOptions{Window: time.Minute, GlobalLimit: 300, AuthLimit: 20},
	})
	return request
}

func completeDocument() *knowledgev1.Document {
	return &knowledgev1.Document{
		Id: "0198f0e0-7b6d-7a11-8e21-1123456789ab", Title: "Design", Summary: "Current state", Slug: "design-doc",
		Owner: &knowledgev1.PublicUser{Id: 7, Username: "alice", Avatar: ""}, Access: "owner",
		MetadataRevision: 1, ContentRevision: 0, CreatedAt: "2026-08-02T12:00:00Z", UpdatedAt: "2026-08-02T12:00:00Z",
	}
}

func completeUser() *identityv1.User {
	return &identityv1.User{
		Id: 7, Username: "alice", Email: "alice@example.com", Role: "user", Status: "active",
		TokenVersion: 1, CreatedAt: "2026-08-02T12:00:00Z", UpdatedAt: "2026-08-02T12:00:00Z",
	}
}

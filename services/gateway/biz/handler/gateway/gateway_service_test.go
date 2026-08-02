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
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
	gatewaymodel "github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/model/gateway"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/config"
	gatewaymiddleware "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/kitex/client/callopt"
)

type identityStub struct {
	registered        *identityv1.User
	authentication    *identityv1.Authentication
	registerErr       error
	authenticateErr   error
	registerCalls     int
	authenticateCalls int
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

func TestLoginReturnsBearerAccessToken(t *testing.T) {
	identity := &identityStub{authentication: &identityv1.Authentication{
		User: completeUser(), AccessToken: "signed-token", ExpiresAtUnix: 2000,
	}}
	request := handlerRequest(identity, `{"identifier":"alice","password":"password"}`)
	Login(context.Background(), request)
	if request.Response.StatusCode() != consts.StatusOK {
		t.Fatalf("status = %d, body = %s", request.Response.StatusCode(), request.Response.Body())
	}
	var response gatewaymodel.LoginResponse
	if err := jsoncodec.Unmarshal(request.Response.Body(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data == nil || response.Data.AccessToken != "signed-token" || response.Data.TokenType != "Bearer" || response.Data.User == nil {
		t.Fatalf("response = %#v", response)
	}
}
func (s *identityStub) GetUser(context.Context, *identityv1.GetUserRequest, ...callopt.Option) (*identityv1.User, error) {
	return nil, nil
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
	var response gatewaymodel.RegisterResponse
	if err := jsoncodec.Unmarshal(request.Response.Body(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != 0 || response.Data == nil || response.Data.ID != 7 || response.RequestID == "" {
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

func TestDocumentHandlersRemainUnimplemented(t *testing.T) {
	request := app.NewContext(0)
	ListPublishedDocuments(context.Background(), request)
	if request.Response.StatusCode() != consts.StatusNotImplemented {
		t.Fatalf("status = %d", request.Response.StatusCode())
	}
}

func handlerRequest(identity gatewaymiddleware.IdentityClient, body string) *app.RequestContext {
	request := app.NewContext(0)
	request.Request.SetBodyString(body)
	request.Set("gateway.dependencies", &gatewaymiddleware.Dependencies{
		Identity: identity, Verifier: verifierStub{}, Limiter: limiterStub{}, Health: health.NewRegistry(),
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		RateLimit: config.RateLimitOptions{Window: time.Minute, GlobalLimit: 300, AuthLimit: 20},
	})
	return request
}

func completeUser() *identityv1.User {
	return &identityv1.User{
		Id: 7, Username: "alice", Email: "alice@example.com", Role: "user", Status: "active",
		TokenVersion: 1, CreatedAtUnix: 1, UpdatedAtUnix: 1,
	}
}

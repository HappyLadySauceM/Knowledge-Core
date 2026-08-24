package rpc

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	platformv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/platform"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/repository"
	platformservice "github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/service"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

type handlerStore struct{ snapshot domain.Snapshot }

func (s *handlerStore) Get(context.Context, string) (domain.Snapshot, error) { return s.snapshot, nil }
func (s *handlerStore) GetRevision(context.Context, string, int64) (domain.Snapshot, error) {
	return s.snapshot, nil
}
func (s *handlerStore) Put(context.Context, repository.PutRequest) (domain.Snapshot, error) {
	return s.snapshot, nil
}
func (s *handlerStore) GetDelivery(context.Context, string, int64) (repository.Delivery, error) {
	return repository.Delivery{}, nil
}
func (s *handlerStore) ConsumerState(context.Context, string, string) (domain.ConsumerState, error) {
	return domain.ConsumerState{Environment: "dev", Namespace: "site", Consumer: "identity.site", DesiredRevision: 1, Status: "pending"}, nil
}
func (s *handlerStore) ReportDelivery(context.Context, domain.DeliveryUpdate) error { return nil }

type readyStub struct{ err error }

func (s readyStub) Ready(context.Context) error { return s.err }

func TestConfigurationRequiresAdministratorToken(t *testing.T) {
	t.Parallel()

	keys, err := coreauth.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	issuer, err := coreauth.NewIssuer(keys.PrivateKey, time.Minute)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	verifier, err := coreauth.NewVerifier(keys.PublicKey)
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	service, err := platformservice.New(&handlerStore{snapshot: domain.Snapshot{Environment: "dev", Namespace: "site", Public: map[string]string{"title": "Site"}, Secrets: map[string]string{}}})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	handler, err := NewHandler(service, verifier, readyStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)), "internal")
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	request := &platformv1.GetConfigurationRequest{Namespace: "site"}
	if _, err := handler.GetConfiguration(context.Background(), request); businessCode(err) != platformv1.CodeUnauthenticated {
		t.Fatalf("GetConfiguration(no token) error = %v", err)
	}
	userToken, err := issuer.Issue(coreauth.Principal{UserID: 7, Role: "user", TokenVersion: 1})
	if err != nil {
		t.Fatalf("Issue(user) error = %v", err)
	}
	if _, err := handler.GetConfiguration(coreauth.WithAccessToken(context.Background(), userToken.Value), request); businessCode(err) != platformv1.CodeForbidden {
		t.Fatalf("GetConfiguration(user) error = %v", err)
	}
	adminToken, err := issuer.Issue(coreauth.Principal{UserID: 42, Role: "admin", TokenVersion: 1})
	if err != nil {
		t.Fatalf("Issue(admin) error = %v", err)
	}
	configuration, err := handler.GetConfiguration(coreauth.WithAccessToken(context.Background(), adminToken.Value), request)
	if err != nil || configuration == nil || configuration.Namespace != "site" {
		t.Fatalf("GetConfiguration(admin) = %#v, %v", configuration, err)
	}
}

func businessCode(err error) int32 {
	business, ok := kerrors.FromBizStatusError(err)
	if !ok {
		return 0
	}
	return business.BizStatusCode()
}

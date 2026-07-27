package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/database"
	foundationpostgres "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/database/postgres"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/app"
	migrationpostgres "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/migration/postgres"
	identitypostgres "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository/postgres"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
	"golang.org/x/crypto/bcrypt"
)

func TestUserRepositoryIntegration(t *testing.T) {
	dsn := os.Getenv("KC_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("KC_TEST_DATABASE_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrationpostgres.Up(ctx, dsn); err != nil {
		t.Fatalf("migration Up() error = %v", err)
	}
	db, err := foundationpostgres.NewProvider().Open(ctx, database.Config{
		DSN:          dsn,
		MaxOpenConns: 4,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	users, err := identitypostgres.NewUserRepository(db)
	if err != nil {
		t.Fatalf("NewUserRepository() error = %v", err)
	}
	hasher, _ := security.NewBcryptHasher(bcrypt.MinCost)
	service, _ := app.NewService(users, hasher)
	suffix := time.Now().UTC().UnixNano()
	username := fmt.Sprintf("user_%d", suffix)
	email := fmt.Sprintf("user_%d@example.com", suffix)
	defer func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM identity.users WHERE lower(email) = lower($1)`, email)
	}()

	registered, err := service.Register(ctx, app.RegisterInput{
		Username: username,
		Email:    email,
		Password: "correct-password",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if registered.ID == 0 || registered.PasswordHash == "correct-password" {
		t.Fatalf("Register() user = %#v", registered)
	}

	_, err = service.Register(ctx, app.RegisterInput{
		Username: username,
		Email:    fmt.Sprintf("other_%d@example.com", suffix),
		Password: "correct-password",
	})
	if !errors.Is(err, app.ErrUsernameConflict) {
		t.Fatalf("duplicate Register() error = %v", err)
	}

	authenticated, err := service.Authenticate(ctx, app.AuthenticateInput{
		Identifier: email,
		Password:   "correct-password",
	})
	if err != nil || authenticated.ID != registered.ID {
		t.Fatalf("Authenticate() = %#v, %v", authenticated, err)
	}

	for attempt := 1; attempt <= 4; attempt++ {
		_, err = service.Authenticate(ctx, app.AuthenticateInput{Identifier: username, Password: "wrong-password"})
		if !errors.Is(err, app.ErrInvalidCredentials) {
			t.Fatalf("Authenticate() attempt %d error = %v", attempt, err)
		}
	}
	_, err = service.Authenticate(ctx, app.AuthenticateInput{Identifier: username, Password: "wrong-password"})
	if !errors.Is(err, app.ErrAccountLocked) {
		t.Fatalf("Authenticate() fifth attempt error = %v", err)
	}
}

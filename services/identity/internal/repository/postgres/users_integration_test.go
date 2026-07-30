package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/database"
	postgresadapter "github.com/HappyLadySauce/Knowledge-Core/internal/database/postgres"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	migrationpostgres "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/migration/postgres"
	identitypostgres "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository/postgres"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
	"golang.org/x/crypto/bcrypt"
)

func TestUserRepositoryIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := openTestDatabase(t, ctx)

	users, err := identitypostgres.NewUserRepository(db)
	if err != nil {
		t.Fatalf("NewUserRepository() error = %v", err)
	}
	hasher, _ := security.NewBcryptHasher(bcrypt.MinCost)
	service, _ := app.NewService(users, hasher, integrationTokenIssuer{})
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
	if err != nil || authenticated.User.ID != registered.ID || authenticated.AccessToken.Value == "" {
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

func TestCreateFirstAdminSerializesConcurrentReplicas(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := openTestDatabase(t, ctx)

	var existingAdmins int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM identity.users WHERE role = 'admin'`).Scan(&existingAdmins); err != nil {
		t.Fatalf("count existing administrators: %v", err)
	}
	if existingAdmins != 0 {
		t.Skip("identity integration database already contains an administrator")
	}
	users, err := identitypostgres.NewUserRepository(db)
	if err != nil {
		t.Fatalf("NewUserRepository() error = %v", err)
	}
	suffix := time.Now().UTC().UnixNano()
	candidates := []*domain.User{
		newAdmin(t, fmt.Sprintf("admin_a_%d", suffix), fmt.Sprintf("admin_a_%d@example.com", suffix)),
		newAdmin(t, fmt.Sprintf("admin_b_%d", suffix), fmt.Sprintf("admin_b_%d@example.com", suffix)),
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM identity.users WHERE email IN ($1, $2)`, candidates[0].Email, candidates[1].Email)
	}()

	type result struct {
		created bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, len(candidates))
	var workers sync.WaitGroup
	for _, candidate := range candidates {
		workers.Add(1)
		go func(user *domain.User) {
			defer workers.Done()
			<-start
			created, createErr := users.CreateFirstAdmin(ctx, user)
			results <- result{created: created, err: createErr}
		}(candidate)
	}
	close(start)
	workers.Wait()
	close(results)

	createdCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("CreateFirstAdmin() error = %v", result.err)
		}
		if result.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created administrators = %d, want 1", createdCount)
	}
	var persistedCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM identity.users WHERE email IN ($1, $2)`, candidates[0].Email, candidates[1].Email).Scan(&persistedCount); err != nil {
		t.Fatalf("count persisted bootstrap administrators: %v", err)
	}
	if persistedCount != 1 {
		t.Fatalf("persisted bootstrap administrators = %d, want 1", persistedCount)
	}
}

func TestCompleteLoginSuccessPreservesConcurrentLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := openTestDatabase(t, ctx)
	users, err := identitypostgres.NewUserRepository(db)
	if err != nil {
		t.Fatalf("NewUserRepository() error = %v", err)
	}
	suffix := time.Now().UTC().UnixNano()
	user, err := domain.NewUser(fmt.Sprintf("login_%d", suffix), fmt.Sprintf("login_%d@example.com", suffix))
	if err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	user.PasswordHash = "hashed-password"
	if err := users.Create(ctx, user); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM identity.users WHERE id = $1`, user.ID)
	}()

	blocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() { _ = blocker.Rollback() }()
	var lockedID int64
	if err := blocker.QueryRowContext(ctx, `SELECT id FROM identity.users WHERE id = $1 FOR UPDATE`, user.ID).Scan(&lockedID); err != nil {
		t.Fatalf("lock user row: %v", err)
	}
	now := time.Now().UTC()
	completed := make(chan struct {
		user *domain.User
		err  error
	}, 1)
	go func() {
		latest, completeErr := users.CompleteLoginSuccess(ctx, user.ID, now)
		completed <- struct {
			user *domain.User
			err  error
		}{user: latest, err: completeErr}
	}()
	lockedUntil := now.Add(15 * time.Minute)
	if _, err := blocker.ExecContext(ctx, `
        UPDATE identity.users
        SET failed_login_attempts = 5, locked_until = $2
        WHERE id = $1`, user.ID, lockedUntil); err != nil {
		t.Fatalf("establish concurrent lock: %v", err)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatalf("commit concurrent lock: %v", err)
	}

	result := <-completed
	if result.err != nil {
		t.Fatalf("CompleteLoginSuccess() error = %v", result.err)
	}
	if result.user == nil || !result.user.IsLocked(now) || result.user.FailedLoginAttempts != 5 {
		t.Fatalf("CompleteLoginSuccess() user = %#v", result.user)
	}
	persisted, err := users.FindByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if !persisted.IsLocked(now) || persisted.FailedLoginAttempts != 5 {
		t.Fatalf("persisted user = %#v", persisted)
	}
}

func openTestDatabase(t *testing.T, ctx context.Context) database.DB {
	t.Helper()
	dsn := os.Getenv("KC_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("KC_TEST_DATABASE_DSN is not set")
	}
	if err := migrationpostgres.Up(ctx, dsn); err != nil {
		t.Fatalf("migration Up() error = %v", err)
	}
	db, err := postgresadapter.NewProvider().Open(ctx, database.Config{
		DSN:          dsn,
		MaxOpenConns: 4,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newAdmin(t *testing.T, username, email string) *domain.User {
	t.Helper()
	user, err := domain.NewUser(username, email)
	if err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	user.Role = domain.RoleAdmin
	user.PasswordHash = "hashed-password"
	return user
}

type integrationTokenIssuer struct{}

func (integrationTokenIssuer) Issue(user *domain.User) (app.AccessToken, error) {
	return app.AccessToken{Value: "integration-access-token", ExpiresAt: user.UpdatedAt.Add(15 * time.Minute)}, nil
}

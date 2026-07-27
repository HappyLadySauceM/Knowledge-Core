package domain_test

import (
	"errors"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
)

func TestNewUserNormalizesAndDefaults(t *testing.T) {
	user, err := domain.NewUser("  Alice_01  ", "  ALICE@example.com ")
	if err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	if user.Username != "Alice_01" || user.Email != "alice@example.com" {
		t.Fatalf("NewUser() normalized user = %#v", user)
	}
	if user.Role != domain.RoleUser || user.Status != domain.StatusActive || user.TokenVersion != 1 {
		t.Fatalf("NewUser() defaults = %#v", user)
	}
}

func TestUserValidation(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "short username", err: domain.ValidateUsername("ab")},
		{name: "invalid username", err: domain.ValidateUsername("alice!")},
		{name: "invalid email", err: domain.ValidateEmail("Alice <alice@example.com>")},
		{name: "short password", err: domain.ValidatePassword("short")},
		{name: "long password", err: domain.ValidatePassword(string(make([]byte, 73)))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var validationError *domain.ValidationError
			if !errors.As(test.err, &validationError) {
				t.Fatalf("error = %v, want ValidationError", test.err)
			}
		})
	}
}

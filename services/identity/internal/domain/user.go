package domain

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"

	StatusActive   = "active"
	StatusDisabled = "disabled"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type User struct {
	ID                  int64
	Username            string
	Email               string
	PasswordHash        string
	Role                string
	Status              string
	TokenVersion        int64
	Avatar              string
	Bio                 string
	FailedLoginAttempts int
	LockedUntil         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validate %s: %s", e.Field, e.Reason)
}

func NewUser(username, email string) (*User, error) {
	username = strings.TrimSpace(username)
	email = strings.ToLower(strings.TrimSpace(email))
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := ValidateEmail(email); err != nil {
		return nil, err
	}
	return &User{
		Username:     username,
		Email:        email,
		Role:         RoleUser,
		Status:       StatusActive,
		TokenVersion: 1,
	}, nil
}

func ValidateUsername(username string) error {
	switch {
	case len(username) < 3 || len(username) > 32:
		return &ValidationError{Field: "username", Reason: "must contain between 3 and 32 characters"}
	case !usernamePattern.MatchString(username):
		return &ValidationError{Field: "username", Reason: "may contain only letters, numbers, underscores, and hyphens"}
	default:
		return nil
	}
}

func ValidateEmail(email string) error {
	if email == "" || len(email) > 320 {
		return &ValidationError{Field: "email", Reason: "must be a valid email address"}
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Name != "" || !strings.EqualFold(address.Address, email) {
		return &ValidationError{Field: "email", Reason: "must be a valid email address"}
	}
	return nil
}

func ValidatePassword(password string) error {
	length := len([]byte(password))
	if length < 8 || length > 72 {
		return &ValidationError{Field: "password", Reason: "must contain between 8 and 72 bytes"}
	}
	return nil
}

func (u *User) IsLocked(now time.Time) bool {
	return u != nil && u.LockedUntil != nil && u.LockedUntil.After(now)
}

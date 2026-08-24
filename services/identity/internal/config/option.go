package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
	"golang.org/x/crypto/bcrypt"
)

// BcryptOptions is Identity-specific because password hashing is not a shared
// infrastructure concern.
type BcryptOptions struct {
	Cost int `mapstructure:"cost" json:"cost" yaml:"cost"`
}

type SMTPOptions struct {
	Host            string `mapstructure:"host" json:"host" yaml:"host"`
	Port            int    `mapstructure:"port" json:"port" yaml:"port"`
	Username        string `mapstructure:"username" json:"username" yaml:"username"`
	Password        string `mapstructure:"password" json:"password" yaml:"password"`
	From            string `mapstructure:"from" json:"from" yaml:"from"`
	FrontendBaseURL string `mapstructure:"frontend_base_url" json:"frontend_base_url" yaml:"frontend_base_url"`
}

func NewSMTPOptions() *SMTPOptions { return &SMTPOptions{Port: 587} }
func (o SMTPOptions) Validate() error {
	if o.Host == "" {
		return nil
	}
	if o.Port <= 0 || o.Port > 65535 || o.From == "" || o.FrontendBaseURL == "" {
		return fmt.Errorf("smtp requires valid port, from, and frontend_base_url when host is configured")
	}
	if o.Port != 465 && o.Port != 587 {
		return fmt.Errorf("smtp port must be 465 or 587")
	}
	if o.Username == "" || o.Password == "" {
		return fmt.Errorf("smtp username and password are required when host is configured")
	}
	frontend, err := url.Parse(strings.TrimSpace(o.FrontendBaseURL))
	if err != nil || frontend.Host == "" || frontend.User != nil || (frontend.Scheme != "http" && frontend.Scheme != "https") || frontend.RawQuery != "" || frontend.Fragment != "" {
		return fmt.Errorf("smtp frontend_base_url must be an absolute HTTP origin without credentials or query")
	}
	return nil
}

type AuthOptions struct {
	PrivateKey                string        `mapstructure:"private_key" json:"private_key" yaml:"private_key"`
	PublicKey                 string        `mapstructure:"public_key" json:"public_key" yaml:"public_key"`
	AccessTokenTTL            time.Duration `mapstructure:"access_token_ttl" json:"access_token_ttl" yaml:"access_token_ttl"`
	RefreshTokenTTL           time.Duration `mapstructure:"refresh_token_ttl" json:"refresh_token_ttl" yaml:"refresh_token_ttl"`
	SessionIdleTTL            time.Duration `mapstructure:"session_idle_ttl" json:"session_idle_ttl" yaml:"session_idle_ttl"`
	ActionTokenTTL            time.Duration `mapstructure:"action_token_ttl" json:"action_token_ttl" yaml:"action_token_ttl"`
	FailureThreshold          int           `mapstructure:"failure_threshold" json:"failure_threshold" yaml:"failure_threshold"`
	LockDuration              time.Duration `mapstructure:"lock_duration" json:"lock_duration" yaml:"lock_duration"`
	ActionTokenPepper         string        `mapstructure:"action_token_pepper" json:"action_token_pepper" yaml:"action_token_pepper"`
	RefreshTokenPepper        string        `mapstructure:"refresh_token_pepper" json:"refresh_token_pepper" yaml:"refresh_token_pepper"`
	RefreshTokenEncryptionKey string        `mapstructure:"refresh_token_encryption_key" json:"refresh_token_encryption_key" yaml:"refresh_token_encryption_key"`
	EmailEncryptionKey        string        `mapstructure:"email_encryption_key" json:"email_encryption_key" yaml:"email_encryption_key"`
	PlatformServiceToken      string        `mapstructure:"platform_service_token" json:"platform_service_token" yaml:"platform_service_token"`
}

func NewAuthOptions() *AuthOptions {
	return &AuthOptions{
		AccessTokenTTL:   15 * time.Minute,
		RefreshTokenTTL:  30 * 24 * time.Hour,
		SessionIdleTTL:   7 * 24 * time.Hour,
		FailureThreshold: 5,
		LockDuration:     15 * time.Minute,
		ActionTokenTTL:   30 * time.Minute,
	}
}

func (o AuthOptions) Validate() error {
	var platformTokenErr error
	if strings.TrimSpace(o.PlatformServiceToken) == "" {
		platformTokenErr = fmt.Errorf("platform_service_token is required")
	}
	var thresholdErr error
	if o.FailureThreshold < 2 || o.FailureThreshold > 100 {
		thresholdErr = fmt.Errorf("failure_threshold must be between 2 and 100, got %d", o.FailureThreshold)
	}
	var ttlErr error
	if o.AccessTokenTTL <= 0 {
		ttlErr = fmt.Errorf("access_token_ttl must be positive")
	}
	var refreshErr error
	if o.RefreshTokenTTL <= 0 {
		refreshErr = fmt.Errorf("refresh_token_ttl must be positive")
	}
	var idleErr error
	if o.SessionIdleTTL <= 0 || o.SessionIdleTTL > o.RefreshTokenTTL {
		idleErr = fmt.Errorf("session_idle_ttl must be positive and no greater than refresh_token_ttl")
	}
	var lockErr error
	if o.LockDuration <= 0 {
		lockErr = fmt.Errorf("lock_duration must be positive")
	}
	var actionErr error
	if o.ActionTokenTTL <= 0 {
		actionErr = fmt.Errorf("action_token_ttl must be positive")
	}
	return errors.Join(
		platformTokenErr,
		thresholdErr,
		ttlErr,
		lockErr,
		refreshErr,
		idleErr,
		actionErr,
		coreauth.ValidateKeyPair(o.PrivateKey, o.PublicKey),
	)
}

func NewBcryptOptions() *BcryptOptions {
	return &BcryptOptions{Cost: security.DefaultBcryptCost}
}

func (o BcryptOptions) Validate() error {
	if o.Cost < bcrypt.MinCost || o.Cost > bcrypt.MaxCost {
		return fmt.Errorf("cost must be between %d and %d, got %d", bcrypt.MinCost, bcrypt.MaxCost, o.Cost)
	}
	return nil
}

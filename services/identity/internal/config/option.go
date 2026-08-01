package config

import (
	"errors"
	"fmt"
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

type AuthOptions struct {
	PrivateKey       string        `mapstructure:"private_key" json:"private_key" yaml:"private_key"`
	PublicKey        string        `mapstructure:"public_key" json:"public_key" yaml:"public_key"`
	AccessTokenTTL   time.Duration `mapstructure:"access_token_ttl" json:"access_token_ttl" yaml:"access_token_ttl"`
	FailureThreshold int           `mapstructure:"failure_threshold" json:"failure_threshold" yaml:"failure_threshold"`
	LockDuration     time.Duration `mapstructure:"lock_duration" json:"lock_duration" yaml:"lock_duration"`
}

func NewAuthOptions() *AuthOptions {
	return &AuthOptions{
		AccessTokenTTL:   15 * time.Minute,
		FailureThreshold: 5,
		LockDuration:     15 * time.Minute,
	}
}

func (o AuthOptions) Validate() error {
	var thresholdErr error
	if o.FailureThreshold < 2 || o.FailureThreshold > 100 {
		thresholdErr = fmt.Errorf("failure_threshold must be between 2 and 100, got %d", o.FailureThreshold)
	}
	var ttlErr error
	if o.AccessTokenTTL <= 0 {
		ttlErr = fmt.Errorf("access_token_ttl must be positive")
	}
	var lockErr error
	if o.LockDuration <= 0 {
		lockErr = fmt.Errorf("lock_duration must be positive")
	}
	return errors.Join(
		thresholdErr,
		ttlErr,
		lockErr,
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

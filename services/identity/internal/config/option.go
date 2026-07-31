package config

import (
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
	"golang.org/x/crypto/bcrypt"
)

// BcryptOptions is Identity-specific because password hashing is not a shared
// infrastructure concern.
type BcryptOptions struct {
	Cost int `mapstructure:"cost" json:"cost" yaml:"cost"`
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

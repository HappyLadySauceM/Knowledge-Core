package security

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const DefaultBcryptCost = 12

type BcryptHasher struct {
	cost int
}

func NewBcryptHasher(cost int) (*BcryptHasher, error) {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		return nil, fmt.Errorf("configure bcrypt: cost must be between %d and %d", bcrypt.MinCost, bcrypt.MaxCost)
	}
	return &BcryptHasher{cost: cost}, nil
}

func (h *BcryptHasher) Hash(password string) (string, error) {
	value, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(value), nil
}

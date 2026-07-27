package security

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const DefaultBcryptCost = 12

type BcryptHasher struct {
	cost int
}

func NewBcryptHasher(cost int) (*BcryptHasher, error) {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		return nil, fmt.Errorf("create bcrypt hasher: cost must be between %d and %d", bcrypt.MinCost, bcrypt.MaxCost)
	}
	return &BcryptHasher{cost: cost}, nil
}

func (h *BcryptHasher) Hash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func (h *BcryptHasher) Compare(hash, password string) (bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("compare password: %w", err)
	}
	return true, nil
}

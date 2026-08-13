package security

import (
	"fmt"
	"sync/atomic"

	"golang.org/x/crypto/bcrypt"
)

const DefaultBcryptCost = 12

type BcryptHasher struct {
	cost atomic.Int64
}

func NewBcryptHasher(cost int) (*BcryptHasher, error) {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		return nil, fmt.Errorf("configure bcrypt: cost must be between %d and %d", bcrypt.MinCost, bcrypt.MaxCost)
	}
	hasher := new(BcryptHasher)
	hasher.cost.Store(int64(cost))
	return hasher, nil
}

func (h *BcryptHasher) Hash(password string) (string, error) {
	value, err := bcrypt.GenerateFromPassword([]byte(password), int(h.cost.Load()))
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(value), nil
}

func (h *BcryptHasher) SetCost(cost int) error {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		return fmt.Errorf("configure bcrypt: cost must be between %d and %d", bcrypt.MinCost, bcrypt.MaxCost)
	}
	h.cost.Store(int64(cost))
	return nil
}

func (h *BcryptHasher) Compare(hash, password string) (bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err == nil {
		return true, nil
	}
	if err == bcrypt.ErrMismatchedHashAndPassword {
		return false, nil
	}
	return false, fmt.Errorf("compare password: %w", err)
}

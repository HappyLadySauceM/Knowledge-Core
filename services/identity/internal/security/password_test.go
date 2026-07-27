package security_test

import (
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
	"golang.org/x/crypto/bcrypt"
)

func TestBcryptHasher(t *testing.T) {
	hasher, err := security.NewBcryptHasher(bcrypt.MinCost)
	if err != nil {
		t.Fatalf("NewBcryptHasher() error = %v", err)
	}
	hash, err := hasher.Hash("correct-password")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if hash == "correct-password" {
		t.Fatal("Hash() returned plaintext")
	}
	matched, err := hasher.Compare(hash, "correct-password")
	if err != nil || !matched {
		t.Fatalf("Compare(correct) = %t, %v", matched, err)
	}
	matched, err = hasher.Compare(hash, "wrong-password")
	if err != nil || matched {
		t.Fatalf("Compare(wrong) = %t, %v", matched, err)
	}
}

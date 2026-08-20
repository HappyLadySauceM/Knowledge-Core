package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

func NewActionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate identity action token: %w", err)
	}
	return "ka1." + base64.RawURLEncoding.EncodeToString(buf), nil
}

func DigestActionToken(token, pepper string) []byte {
	hash := sha256.New()
	hash.Write([]byte(pepper))
	hash.Write([]byte{0})
	hash.Write([]byte(strings.TrimSpace(token)))
	return hash.Sum(nil)
}

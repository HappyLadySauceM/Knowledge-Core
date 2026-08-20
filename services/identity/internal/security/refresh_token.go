package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	refreshTokenPrefix = "kc1"
	refreshTokenBytes  = 32
	maxRefreshLength   = 256
)

type RefreshToken struct {
	SessionID string
	Secret    string
}

func NewRefreshToken(random io.Reader) (RefreshToken, error) {
	if random == nil {
		random = rand.Reader
	}
	session := make([]byte, 16)
	secret := make([]byte, refreshTokenBytes)
	if _, err := io.ReadFull(random, session); err != nil {
		return RefreshToken{}, fmt.Errorf("generate refresh session ID: %w", err)
	}
	if _, err := io.ReadFull(random, secret); err != nil {
		return RefreshToken{}, fmt.Errorf("generate refresh token secret: %w", err)
	}
	return RefreshToken{SessionID: base64.RawURLEncoding.EncodeToString(session), Secret: base64.RawURLEncoding.EncodeToString(secret)}, nil
}

func (t RefreshToken) Encode() (string, error) {
	if t.SessionID == "" || t.Secret == "" {
		return "", errors.New("encode refresh token: session ID and secret are required")
	}
	return strings.Join([]string{refreshTokenPrefix, t.SessionID, t.Secret}, "."), nil
}

func ParseRefreshToken(value string) (RefreshToken, error) {
	if value == "" || len(value) > maxRefreshLength {
		return RefreshToken{}, errors.New("parse refresh token: token is invalid")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != refreshTokenPrefix || parts[1] == "" || parts[2] == "" {
		return RefreshToken{}, errors.New("parse refresh token: token is invalid")
	}
	if _, err := base64.RawURLEncoding.DecodeString(parts[1]); err != nil {
		return RefreshToken{}, errors.New("parse refresh token: session ID is invalid")
	}
	secret, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(secret) != refreshTokenBytes {
		return RefreshToken{}, errors.New("parse refresh token: secret is invalid")
	}
	return RefreshToken{SessionID: parts[1], Secret: parts[2]}, nil
}

func DigestRefreshSecret(secret string, pepper []byte) []byte {
	hash := hmac.New(sha256.New, pepper)
	_, _ = hash.Write([]byte(secret))
	return hash.Sum(nil)
}

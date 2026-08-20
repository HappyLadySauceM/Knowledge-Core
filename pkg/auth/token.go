// Package auth defines the shared access-token wire contract used by Identity
// and trusted edge services. It contains no application authorization policy.
package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	IssuerName     = "knowledge-core.identity"
	AudienceName   = "knowledge-core.api"
	MaxTokenLength = 4096
)

type Principal struct {
	UserID       int64
	Role         string
	TokenVersion int64
	SessionID    string
	ExpiresAt    time.Time
}

type IssuedToken struct {
	Value     string
	ExpiresAt time.Time
}

type KeyPair struct {
	PrivateKey string
	PublicKey  string
}

func GenerateKeyPair() (KeyPair, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate access token key pair: %w", err)
	}
	return KeyPair{
		PrivateKey: base64.StdEncoding.EncodeToString(privateKey),
		PublicKey:  base64.StdEncoding.EncodeToString(publicKey),
	}, nil
}

func ValidateKeyPair(encodedPrivateKey, encodedPublicKey string) error {
	privateKey, err := parsePrivateKey(encodedPrivateKey)
	if err != nil {
		return err
	}
	publicKey, err := parsePublicKey(encodedPublicKey)
	if err != nil {
		return err
	}
	derived, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok || subtle.ConstantTimeCompare(derived, publicKey) != 1 {
		return errors.New("validate access token key pair: public key does not match private key")
	}
	return nil
}

type Claims struct {
	Role         string `json:"role"`
	TokenVersion int64  `json:"token_version"`
	SessionID    string `json:"session_id,omitempty"`
	jwt.RegisteredClaims
}

func (c Claims) Validate() error {
	userID, err := strconv.ParseInt(c.Subject, 10, 64)
	switch {
	case err != nil || userID <= 0:
		return errors.New("access token subject is invalid")
	case c.Role == "" || len(c.Role) > 32:
		return errors.New("access token role is invalid")
	case c.TokenVersion <= 0:
		return errors.New("access token version is invalid")
	case c.ExpiresAt == nil || c.NotBefore == nil || c.IssuedAt == nil:
		return errors.New("access token timestamps are required")
	case c.ID == "" || len(c.ID) > 128:
		return errors.New("access token ID is invalid")
	default:
		return nil
	}
}

type Issuer struct {
	privateKey ed25519.PrivateKey
	ttl        atomic.Int64
	now        func() time.Time
	random     io.Reader
}

func NewIssuer(encodedPrivateKey string, ttl time.Duration) (*Issuer, error) {
	privateKey, err := parsePrivateKey(encodedPrivateKey)
	if err != nil {
		return nil, err
	}
	if ttl <= 0 {
		return nil, errors.New("create access token issuer: TTL must be positive")
	}
	issuer := &Issuer{privateKey: privateKey, now: time.Now, random: rand.Reader}
	issuer.ttl.Store(int64(ttl))
	return issuer, nil
}

func (i *Issuer) Issue(principal Principal) (IssuedToken, error) {
	if i == nil || len(i.privateKey) != ed25519.PrivateKeySize {
		return IssuedToken{}, errors.New("issue access token: issuer is not configured")
	}
	if principal.UserID <= 0 || principal.Role == "" || len(principal.Role) > 32 || principal.TokenVersion <= 0 {
		return IssuedToken{}, errors.New("issue access token: principal is invalid")
	}
	tokenID, err := randomID(i.random)
	if err != nil {
		return IssuedToken{}, fmt.Errorf("issue access token ID: %w", err)
	}
	now := i.now().UTC()
	expiresAt := now.Add(time.Duration(i.ttl.Load()))
	claims := Claims{
		Role:         principal.Role,
		TokenVersion: principal.TokenVersion,
		SessionID:    principal.SessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    IssuerName,
			Subject:   strconv.FormatInt(principal.UserID, 10),
			Audience:  jwt.ClaimStrings{AudienceName},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        tokenID,
		},
	}
	value, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(i.privateKey)
	if err != nil {
		return IssuedToken{}, fmt.Errorf("sign access token: %w", err)
	}
	return IssuedToken{Value: value, ExpiresAt: expiresAt}, nil
}

func (i *Issuer) SetTTL(ttl time.Duration) error {
	if i == nil || ttl <= 0 {
		return errors.New("set access token TTL: issuer and positive TTL are required")
	}
	i.ttl.Store(int64(ttl))
	return nil
}

type Verifier struct {
	publicKey ed25519.PublicKey
	now       func() time.Time
}

func NewVerifier(encodedPublicKey string) (*Verifier, error) {
	publicKey, err := parsePublicKey(encodedPublicKey)
	if err != nil {
		return nil, err
	}
	return &Verifier{publicKey: publicKey, now: time.Now}, nil
}

func (v *Verifier) Verify(value string) (Principal, error) {
	if v == nil || len(v.publicKey) != ed25519.PublicKeySize {
		return Principal{}, errors.New("verify access token: verifier is not configured")
	}
	value = strings.TrimSpace(value)
	if value == "" || len(value) > MaxTokenLength {
		return Principal{}, errors.New("verify access token: token is invalid")
	}
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(
		value,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodEdDSA {
				return nil, errors.New("unexpected signing method")
			}
			return v.publicKey, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(IssuerName),
		jwt.WithAudience(AudienceName),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithStrictDecoding(),
		jwt.WithTimeFunc(v.now),
	)
	if err != nil || !token.Valid {
		return Principal{}, errors.New("verify access token: token is invalid")
	}
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return Principal{}, errors.New("verify access token: subject is invalid")
	}
	return Principal{
		UserID: userID, Role: claims.Role, TokenVersion: claims.TokenVersion, SessionID: claims.SessionID,
		ExpiresAt: claims.ExpiresAt.UTC(),
	}, nil
}

func parsePrivateKey(encoded string) (ed25519.PrivateKey, error) {
	data, err := decodeKey(encoded, "private")
	if err != nil {
		return nil, err
	}
	switch len(data) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(data), nil
	case ed25519.PrivateKeySize:
		return append(ed25519.PrivateKey(nil), data...), nil
	default:
		key, parseErr := x509.ParsePKCS8PrivateKey(data)
		privateKey, ok := key.(ed25519.PrivateKey)
		if parseErr != nil || !ok {
			return nil, errors.New("parse access token private key: expected Ed25519 raw or PKCS#8 key")
		}
		return append(ed25519.PrivateKey(nil), privateKey...), nil
	}
}

func parsePublicKey(encoded string) (ed25519.PublicKey, error) {
	data, err := decodeKey(encoded, "public")
	if err != nil {
		return nil, err
	}
	if len(data) == ed25519.PublicKeySize {
		return append(ed25519.PublicKey(nil), data...), nil
	}
	key, parseErr := x509.ParsePKIXPublicKey(data)
	publicKey, ok := key.(ed25519.PublicKey)
	if parseErr != nil || !ok {
		return nil, errors.New("parse access token public key: expected Ed25519 raw or PKIX key")
	}
	return append(ed25519.PublicKey(nil), publicKey...), nil
}

func decodeKey(encoded, kind string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, fmt.Errorf("parse access token %s key: value is required", kind)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		return nil, fmt.Errorf("parse access token %s key: invalid base64", kind)
	}
	return data, nil
}

func randomID(source io.Reader) (string, error) {
	var value [16]byte
	if _, err := io.ReadFull(source, value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

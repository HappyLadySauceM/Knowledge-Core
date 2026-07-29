package security

import (
	"errors"
	"time"

	auth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
)

const AccessTokenTTL = 15 * time.Minute

type AccessTokenIssuer struct {
	issuer *auth.Issuer
}

func NewAccessTokenIssuer(encodedPrivateKey string) (*AccessTokenIssuer, error) {
	issuer, err := auth.NewIssuer(encodedPrivateKey, AccessTokenTTL)
	if err != nil {
		return nil, err
	}
	return &AccessTokenIssuer{issuer: issuer}, nil
}

func (i *AccessTokenIssuer) Issue(user *domain.User) (app.AccessToken, error) {
	if i == nil || i.issuer == nil || user == nil {
		return app.AccessToken{}, errors.New("issue identity access token: issuer and user are required")
	}
	issued, err := i.issuer.Issue(auth.Principal{
		UserID:       user.ID,
		Role:         string(user.Role),
		TokenVersion: user.TokenVersion,
	})
	if err != nil {
		return app.AccessToken{}, err
	}
	return app.AccessToken{Value: issued.Value, ExpiresAt: issued.ExpiresAt}, nil
}

var _ app.AccessTokenIssuer = (*AccessTokenIssuer)(nil)

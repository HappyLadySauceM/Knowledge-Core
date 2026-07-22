package auth

import (
	v1 "github.com/HappyLadySauce/Knowledge-Core/cmd/app/types/v1"
	internalauth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	internaluser "github.com/HappyLadySauce/Knowledge-Core/internal/user"
)

func toTokenResponse(token internalauth.TokenResponse) v1.TokenResponse {
	return v1.TokenResponse{
		AccessToken:  token.AccessToken,
		TokenType:    token.TokenType,
		ExpiresIn:    token.ExpiresIn,
		RefreshToken: token.RefreshToken,
		Scope:        token.Scope,
		User:         toUserResponse(token.User),
	}
}

func toUserResponse(user internaluser.User) v1.UserResponse {
	return v1.UserResponse{
		ID:                 user.ID,
		Username:           user.Username,
		Email:              user.Email,
		Avatar:             user.Avatar,
		Bio:                user.Bio,
		Role:               user.Role,
		Status:             user.Status,
		MustChangePassword: user.MustChangePassword,
		CreatedAt:          user.CreatedAt,
		UpdatedAt:          user.UpdatedAt,
	}
}

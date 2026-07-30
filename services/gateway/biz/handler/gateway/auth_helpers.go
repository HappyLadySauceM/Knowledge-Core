package gateway

import (
	identityrpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	gatewaymodel "github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/model/gateway"
)

func mapUser(user *identityrpc.User) *gatewaymodel.UserData {
	if user == nil {
		return nil
	}
	return &gatewaymodel.UserData{
		ID:            user.Id,
		Username:      user.Username,
		Email:         user.Email,
		Role:          user.Role,
		Status:        user.Status,
		Avatar:        user.Avatar,
		Bio:           user.Bio,
		CreatedAtUnix: user.CreatedAtUnix,
		UpdatedAtUnix: user.UpdatedAtUnix,
	}
}

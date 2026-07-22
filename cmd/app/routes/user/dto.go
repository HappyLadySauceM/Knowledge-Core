package user

import (
	v1 "github.com/HappyLadySauce/Knowledge-Core/cmd/app/types/v1"
	internaluser "github.com/HappyLadySauce/Knowledge-Core/internal/user"
)

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

func toListUsersResponse(result internaluser.ListResult) v1.ListUsersResponse {
	items := make([]v1.UserResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, toUserResponse(item))
	}
	return v1.ListUsersResponse{
		Items:    items,
		Total:    result.Total,
		Page:     result.Page,
		PageSize: result.PageSize,
	}
}

package v1

import "time"

type UserResponse struct {
	ID                 int64     `json:"id"`
	Username           string    `json:"username"`
	Email              string    `json:"email,omitempty"`
	Avatar             string    `json:"avatar"`
	Bio                string    `json:"bio"`
	Role               string    `json:"role"`
	Status             string    `json:"status"`
	MustChangePassword bool      `json:"must_change_password"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type UpdateMeRequest struct {
	Username *string `json:"username"`
	Email    *string `json:"email"`
	Avatar   *string `json:"avatar"`
	Bio      *string `json:"bio"`
}

type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

type ListUsersRequest struct {
	Page     int    `form:"page"`
	PageSize int    `form:"page_size"`
	Role     string `form:"role"`
	Status   string `form:"status"`
	Keyword  string `form:"keyword"`
}

type ListUsersResponse struct {
	Items    []UserResponse `json:"items"`
	Total    int64          `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
}

type AdminUpdateUserRequest struct {
	Username *string `json:"username"`
	Email    *string `json:"email"`
	Avatar   *string `json:"avatar"`
	Bio      *string `json:"bio"`
	Status   *string `json:"status"`
	Role     *string `json:"role"`
}

type AdminResetPasswordRequest struct {
	Password string `json:"password" binding:"required"`
}

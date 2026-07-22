package user

import (
	"context"
	"time"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"

	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// User is the domain account model.
// User 是领域层账户模型。
type User struct {
	ID                 int64
	Username           string
	Email              string
	Avatar             string
	Bio                string
	Role               string
	Status             string
	MustChangePassword bool
	TokenVersion       int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Record includes private credential data for password verification.
// Record 包含用于密码校验的私有凭据数据。
type Record struct {
	User
	PasswordHash string
}

type UpdateProfileCommand struct {
	Username *string
	Email    *string
	Avatar   *string
	Bio      *string
}

type AdminUpdateCommand struct {
	Username *string
	Email    *string
	Avatar   *string
	Bio      *string
	Status   *string
	Role     *string
}

type ChangePasswordCommand struct {
	OldPassword string
	NewPassword string
}

type ListQuery struct {
	Page     int
	PageSize int
	Role     string
	Status   string
	Keyword  string
}

type ListResult struct {
	Items    []User
	Total    int64
	Page     int
	PageSize int
}

type UserService interface {
	GetMe(ctx context.Context, actor User) (User, error)
	UpdateMe(ctx context.Context, actor User, cmd UpdateProfileCommand) (User, error)
	ChangePassword(ctx context.Context, actor User, cmd ChangePasswordCommand) error
	ListUsers(ctx context.Context, actor User, query ListQuery) (ListResult, error)
	GetUser(ctx context.Context, actor User, id int64) (User, error)
	UpdateUser(ctx context.Context, actor User, id int64, cmd AdminUpdateCommand) (User, error)
	DeleteUser(ctx context.Context, actor User, id int64) error
	ResetPassword(ctx context.Context, actor User, id int64, password string) error
}

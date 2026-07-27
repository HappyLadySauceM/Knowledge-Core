package gateway

import (
	"context"
	"net/http"

	identityrpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	gatewaymodel "github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/model/gateway"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

const (
	codeInvalidRequest        int32 = 10002
	codeUnauthorized          int32 = 10003
	codeConflict              int32 = 10004
	codeForbidden             int32 = 10005
	codeAccountLocked         int32 = 10006
	codeDependencyUnavailable int32 = 10007
)

func writeError(c *app.RequestContext, status int, code int32, message string) {
	c.JSON(status, &gatewaymodel.ErrorResponse{
		Code:      code,
		Message:   message,
		Data:      &gatewaymodel.EmptyData{},
		RequestID: middleware.GetRequestID(c),
	})
}

func writeIdentityError(ctx context.Context, c *app.RequestContext, err error) {
	bizError, ok := kerrors.FromBizStatusError(err)
	if !ok {
		hlog.CtxErrorf(ctx, "Identity RPC request failed: %v", err)
		writeError(c, http.StatusServiceUnavailable, codeDependencyUnavailable, "service unavailable")
		return
	}
	switch bizError.BizStatusCode() {
	case identityrpc.CodeInvalidInput:
		writeError(c, http.StatusBadRequest, codeInvalidRequest, "invalid request")
	case identityrpc.CodeConflict:
		writeError(c, http.StatusConflict, codeConflict, "account already exists")
	case identityrpc.CodeInvalidCredentials:
		writeError(c, http.StatusUnauthorized, codeUnauthorized, "invalid credentials")
	case identityrpc.CodeAccountLocked:
		writeError(c, http.StatusLocked, codeAccountLocked, "account is temporarily locked")
	case identityrpc.CodeUserDisabled:
		writeError(c, http.StatusForbidden, codeForbidden, "account is disabled")
	case identityrpc.CodeUserNotFound:
		writeError(c, http.StatusNotFound, codeInvalidRequest, "user not found")
	default:
		hlog.CtxErrorf(ctx, "Identity RPC returned business code %d", bizError.BizStatusCode())
		writeError(c, http.StatusBadGateway, codeDependencyUnavailable, "service unavailable")
	}
}

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

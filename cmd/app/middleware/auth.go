package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/HappyLadySauce/Knowledge-Core/cmd/app/svc"
	"github.com/HappyLadySauce/Knowledge-Core/cmd/app/types/common"
	"github.com/HappyLadySauce/Knowledge-Core/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/internal/user"
)

const contextUserKey = "auth.user"

// AuthMiddleware validates Bearer JWT and stores the current user in Gin context.
// AuthMiddleware 校验 Bearer JWT 并将当前用户写入 Gin 上下文。
func AuthMiddleware(sc *svc.ServiceContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := strings.TrimSpace(c.GetHeader("Authorization"))
		if !strings.HasPrefix(header, "Bearer ") {
			common.Error(c, errors.InvalidToken)
			c.Abort()
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		user, err := sc.Auth.CurrentUser(c.Request.Context(), token)
		if err != nil {
			common.Error(c, err)
			c.Abort()
			return
		}
		c.Set(contextUserKey, user)
		c.Next()
	}
}

// RequireAdmin allows only admin users through.
// RequireAdmin 仅允许 admin 用户通过。
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		currentUser, ok := UserFromContext(c)
		if !ok {
			common.Error(c, errors.InvalidToken)
			c.Abort()
			return
		}
		if currentUser.Role != user.RoleAdmin {
			common.Error(c, errors.Forbidden)
			c.Abort()
			return
		}
		if currentUser.MustChangePassword {
			common.Error(c, errors.PasswordChangeRequired)
			c.Abort()
			return
		}
		c.Next()
	}
}

// UserFromContext returns the authenticated user stored by AuthMiddleware.
// UserFromContext 返回 AuthMiddleware 写入上下文的认证用户。
func UserFromContext(c *gin.Context) (user.User, bool) {
	value, ok := c.Get(contextUserKey)
	if !ok {
		return user.User{}, false
	}
	currentUser, ok := value.(user.User)
	return currentUser, ok
}

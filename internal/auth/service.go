package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"k8s.io/klog/v2"

	apperrors "github.com/HappyLadySauce/Knowledge-Core/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/internal/options"
	"github.com/HappyLadySauce/Knowledge-Core/internal/user"
)

const (
	bcryptCost         = 12
	maxLoginAttempts   = 5
	loginLockDuration  = 15 * time.Minute
	defaultKeyPrefix   = "knowledge-core"
	revokeReasonLogout = "logout"
)

// dummyHash is a bcrypt hash of a random value, used to keep Login response time
// constant when the username does not exist (prevents timing-based user enumeration).
// dummyHash 是随机值的 bcrypt 哈希，用于在用户名不存在时保持 Login 响应时间恒定，
// 防止基于时序的用户名枚举攻击。
var dummyHash = func() []byte {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("generate dummy hash: " + err.Error())
	}
	h, err := bcrypt.GenerateFromPassword(b, bcryptCost)
	if err != nil {
		panic("generate dummy hash: " + err.Error())
	}
	return h
}()

// Service implements auth use cases.
// Service 实现认证服务。
type Service struct {
	db       *sql.DB
	redis    *redis.Client
	jwt      *options.JWTOptions
	users    *user.Repository
	attempts *Repository

	keyPrefix string
}

// ServiceOptions controls auth service internals.
// ServiceOptions 控制认证服务内部选项。
type ServiceOptions struct {
	KeyPrefix string
}

// NewService creates an auth service.
// NewService 创建认证服务。
func NewService(db *sql.DB, jwt *options.JWTOptions, redisClient *redis.Client, opts ...ServiceOptions) *Service {
	serviceOptions := normalizeServiceOptions(opts...)
	return &Service{
		db:        db,
		redis:     redisClient,
		jwt:       jwt,
		users:     user.NewRepository(db),
		attempts:  NewRepository(db),
		keyPrefix: serviceOptions.KeyPrefix,
	}
}

func normalizeServiceOptions(opts ...ServiceOptions) ServiceOptions {
	options := ServiceOptions{KeyPrefix: defaultKeyPrefix}
	if len(opts) > 0 {
		options = opts[0]
	}
	options.KeyPrefix = strings.Trim(strings.TrimSpace(options.KeyPrefix), ":")
	if options.KeyPrefix == "" {
		options.KeyPrefix = defaultKeyPrefix
	}
	return options
}

// EnsureAdmin creates the initial admin user when none exists. The password is
// read from KNOWLEDGE_CORE_ADMIN_PASSWORD; if unset, a random password is generated
// and logged once. This replaces the deprecated hardcoded hash in migrations.
// EnsureAdmin 在不存在 admin 用户时创建初始管理员。密码从 KNOWLEDGE_CORE_ADMIN_PASSWORD
// 读取；若未设置则生成随机密码并记录日志一次。替代迁移脚本中已废弃的硬编码哈希。
func (s *Service) EnsureAdmin(ctx context.Context) error {
	const adminUsername = "admin"
	_, err := s.users.GetRecordByUsername(ctx, adminUsername)
	if err == nil {
		return nil
	}
	if !apperrors.Is(err, apperrors.InvalidCredentials) {
		return err
	}

	password := os.Getenv("KNOWLEDGE_CORE_ADMIN_PASSWORD")
	generated := false
	if password == "" {
		b := make([]byte, 16)
		if _, randErr := rand.Read(b); randErr != nil {
			return apperrors.Wrap(apperrors.InternalError, randErr)
		}
		password = hex.EncodeToString(b)
		generated = true
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return apperrors.Wrap(apperrors.InternalError, err)
	}
	if _, err := s.users.CreateWithRole(ctx, adminUsername, "", string(hash), user.RoleAdmin, true); err != nil {
		return err
	}
	if generated {
		klog.InfoS("bootstrap admin user created with generated password; set KNOWLEDGE_CORE_ADMIN_PASSWORD to control it",
			"username", adminUsername,
			"password", password)
	} else {
		klog.InfoS("bootstrap admin user created from KNOWLEDGE_CORE_ADMIN_PASSWORD; password change required on first login",
			"username", adminUsername)
	}
	return nil
}

// Register creates an active user account and immediately issues tokens.
// Register 创建 active 普通用户账号并立即签发令牌。
func (s *Service) Register(ctx context.Context, req RegisterCommand) (TokenResponse, error) {
	username := user.NormalizeUsername(req.Username)
	password := strings.TrimSpace(req.Password)
	email := strings.TrimSpace(req.Email)
	if username == "" || password == "" {
		return TokenResponse{}, apperrors.InvalidRequest
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return TokenResponse{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	created, err := s.users.Create(ctx, username, email, string(hash))
	if err != nil {
		return TokenResponse{}, err
	}
	return s.issueTokenResponse(ctx, created)
}

// Login verifies credentials and issues tokens.
// Login 校验凭据并签发令牌。
func (s *Service) Login(ctx context.Context, req LoginCommand) (TokenResponse, error) {
	record, err := s.users.GetRecordByUsername(ctx, user.NormalizeUsername(req.Username))
	if err != nil {
		if apperrors.Is(err, apperrors.InvalidCredentials) {
			// Username not found: run a dummy bcrypt compare so response time
			// matches the found case, preventing timing-based user enumeration.
			// 用户名不存在：执行虚拟 bcrypt 比较使响应时间与存在时一致，防止时序枚举。
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
		}
		return TokenResponse{}, err
	}

	// Check account lock before verifying credentials.
	// 验证凭据前检查账户锁定状态。
	attempt, err := s.attempts.GetLoginAttempt(ctx, record.ID)
	if err != nil {
		return TokenResponse{}, err
	}
	if attempt.LockedUntil.Valid && time.Now().Before(attempt.LockedUntil.Time) {
		return TokenResponse{}, apperrors.UserLocked
	}

	if bcrypt.CompareHashAndPassword([]byte(record.PasswordHash), []byte(req.Password)) != nil {
		if lErr := s.attempts.RecordFailedLogin(ctx, record.ID); lErr != nil {
			return TokenResponse{}, lErr
		}
		return TokenResponse{}, apperrors.InvalidCredentials
	}
	if record.Status != user.StatusActive {
		return TokenResponse{}, apperrors.UserDisabled
	}

	// Successful login: clear any previous failure state.
	// 登录成功：清除之前的失败记录。
	if err := s.attempts.ResetLoginAttempt(ctx, record.ID); err != nil {
		return TokenResponse{}, err
	}
	return s.issueTokenResponse(ctx, record.User)
}

// Refresh rotates a refresh token and issues a new token response.
// Refresh 轮换刷新令牌并签发新的令牌响应。
func (s *Service) Refresh(ctx context.Context, req RefreshCommand) (TokenResponse, error) {
	plain := strings.TrimSpace(req.RefreshToken)
	if plain == "" {
		return TokenResponse{}, apperrors.InvalidToken
	}
	newPlain, newHash, err := newRefreshToken()
	if err != nil {
		return TokenResponse{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	currentUser, err := s.rotateRefreshToken(ctx, refreshTokenHash(plain), newHash, time.Now().UTC().Add(s.jwt.RefreshTTL))
	if err != nil {
		return TokenResponse{}, err
	}
	accessToken, expiresIn, err := s.issueAccessToken(currentUser)
	if err != nil {
		return TokenResponse{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	return TokenResponse{
		AccessToken:  accessToken,
		TokenType:    TokenTypeBearer,
		ExpiresIn:    expiresIn,
		RefreshToken: newPlain,
		Scope:        scopeForUser(currentUser),
		User:         currentUser,
	}, nil
}

// Logout revokes one refresh token belonging to the authenticated user.
// Logout 撤销属于当前认证用户的单个刷新令牌。
func (s *Service) Logout(ctx context.Context, req LogoutCommand) error {
	plain := strings.TrimSpace(req.RefreshToken)
	if plain == "" || req.UserID <= 0 {
		return apperrors.InvalidToken
	}
	return s.revokeRefreshToken(ctx, req.UserID, refreshTokenHash(plain), revokeReasonLogout)
}

// CurrentUser returns the active user behind an access token.
// CurrentUser 返回访问令牌对应的 active 用户。
func (s *Service) CurrentUser(ctx context.Context, rawToken string) (user.User, error) {
	claims, err := s.parseAccessToken(rawToken)
	if err != nil {
		return user.User{}, err
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return user.User{}, apperrors.InvalidToken
	}
	currentUser, err := s.users.GetByID(ctx, id)
	if err != nil {
		return user.User{}, err
	}
	if currentUser.Status != user.StatusActive {
		return user.User{}, apperrors.UserDisabled
	}
	// Reject tokens issued before the latest token_version bump (JWT blacklist).
	// 拒绝在最近一次 token_version 递增之前签发的令牌（JWT 黑名单）。
	if claims.TokenVersion != currentUser.TokenVersion {
		return user.User{}, apperrors.InvalidToken
	}
	return currentUser, nil
}

func (s *Service) issueTokenResponse(ctx context.Context, currentUser user.User) (TokenResponse, error) {
	accessToken, expiresIn, err := s.issueAccessToken(currentUser)
	if err != nil {
		return TokenResponse{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	refreshPlain, refreshHash, err := newRefreshToken()
	if err != nil {
		return TokenResponse{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	if err := s.storeRefreshToken(ctx, currentUser, refreshHash, time.Now().UTC().Add(s.jwt.RefreshTTL)); err != nil {
		return TokenResponse{}, err
	}
	return TokenResponse{
		AccessToken:  accessToken,
		TokenType:    TokenTypeBearer,
		ExpiresIn:    expiresIn,
		RefreshToken: refreshPlain,
		Scope:        scopeForUser(currentUser),
		User:         currentUser,
	}, nil
}

func scopeForUser(currentUser user.User) string {
	return "role:" + currentUser.Role
}

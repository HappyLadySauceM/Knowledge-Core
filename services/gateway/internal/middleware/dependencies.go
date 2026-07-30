package middleware

import (
	"context"
	"strings"
	"time"

	auth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	"github.com/HappyLadySauce/Knowledge-Core/internal/cache"
	"github.com/HappyLadySauce/Knowledge-Core/internal/health"
	"github.com/HappyLadySauce/Knowledge-Core/internal/observability"
	identityrpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity/identityservice"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge/knowledgeservice"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/hlog"
)

const (
	healthRegistryKey  = "knowledge-core.health-registry"
	cacheStoreKey      = "knowledge-core.cache-store"
	identityClientKey  = "knowledge-core.identity-client"
	knowledgeClientKey = "knowledge-core.knowledge-client"
	principalKey       = "knowledge-core.auth-principal"
	accessTokenKey     = "knowledge-core.auth-access-token"
	identityUserKey    = "knowledge-core.auth-identity-user"
)

type RuntimeDependencies struct {
	Health    *health.Registry
	Cache     cache.KVStore
	Identity  identityservice.Client
	Knowledge knowledgeservice.Client
}

type TokenVerifier interface {
	Verify(value string) (auth.Principal, error)
}

func Dependencies(dependencies RuntimeDependencies) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		request.Set(healthRegistryKey, dependencies.Health)
		request.Set(cacheStoreKey, dependencies.Cache)
		request.Set(identityClientKey, dependencies.Identity)
		request.Set(knowledgeClientKey, dependencies.Knowledge)
		request.Next(ctx)
	}
}

func Authentication(verifier TokenVerifier) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		header := string(request.GetHeader("Authorization"))
		if header != "" && verifier != nil {
			parts := strings.Fields(header)
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				if principal, err := verifier.Verify(parts[1]); err == nil {
					request.Set(principalKey, principal)
					request.Set(accessTokenKey, parts[1])
					ctx = observability.WithUserID(ctx, principal.UserID)
				}
			}
		}
		request.Next(ctx)
	}
}

func RequireAuthenticated() app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		if _, authenticated := refreshAuthenticatedUser(ctx, request); !authenticated {
			return
		}
		request.Next(ctx)
	}
}

func RequireRoles(roles ...string) app.HandlerFunc {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		if role = strings.TrimSpace(role); role != "" {
			allowed[role] = struct{}{}
		}
	}
	return func(ctx context.Context, request *app.RequestContext) {
		_, authenticated := refreshAuthenticatedUser(ctx, request)
		if !authenticated {
			return
		}
		principal, _ := Principal(request)
		if _, exists := allowed[principal.Role]; !exists {
			WriteError(request, ErrPermissionDenied)
			return
		}
		request.Next(ctx)
	}
}

func CacheStore(request *app.RequestContext) (cache.KVStore, bool) {
	value, exists := request.Get(cacheStoreKey)
	store, ok := value.(cache.KVStore)
	return store, exists && ok && store != nil
}

func HealthRegistry(request *app.RequestContext) (*health.Registry, bool) {
	value, exists := request.Get(healthRegistryKey)
	registry, ok := value.(*health.Registry)
	return registry, exists && ok
}

func IdentityClient(request *app.RequestContext) (identityservice.Client, bool) {
	value, exists := request.Get(identityClientKey)
	client, ok := value.(identityservice.Client)
	return client, exists && ok && client != nil
}

func KnowledgeClient(request *app.RequestContext) (knowledgeservice.Client, bool) {
	value, exists := request.Get(knowledgeClientKey)
	client, ok := value.(knowledgeservice.Client)
	return client, exists && ok && client != nil
}

func Principal(request *app.RequestContext) (auth.Principal, bool) {
	value, exists := request.Get(principalKey)
	principal, ok := value.(auth.Principal)
	return principal, exists && ok && principal.UserID > 0
}

func AccessToken(request *app.RequestContext) (string, bool) {
	value, exists := request.Get(accessTokenKey)
	token, ok := value.(string)
	return token, exists && ok && token != ""
}

func IdentityUser(request *app.RequestContext) (*identityrpc.User, bool) {
	value, exists := request.Get(identityUserKey)
	user, ok := value.(*identityrpc.User)
	return user, exists && ok && user != nil
}

func refreshAuthenticatedUser(ctx context.Context, request *app.RequestContext) (*identityrpc.User, bool) {
	principal, authenticated := Principal(request)
	token, hasToken := AccessToken(request)
	if !authenticated || !hasToken {
		WriteError(request, ErrAuthenticationRequired)
		return nil, false
	}
	if user, exists := IdentityUser(request); exists {
		return user, true
	}
	client, exists := IdentityClient(request)
	if !exists {
		hlog.CtxErrorf(ctx, "Identity client is not configured")
		WriteError(request, ErrDependencyUnavailable)
		return nil, false
	}

	rpcCtx, cancel := context.WithTimeout(auth.WithAccessToken(ctx, token), 3*time.Second)
	defer cancel()
	user, err := client.GetUser(rpcCtx, &identityrpc.GetUserRequest{UserId: principal.UserID})
	if err != nil {
		WriteIdentityError(ctx, request, err)
		return nil, false
	}
	if user == nil || user.Role == "" {
		hlog.CtxErrorf(ctx, "Identity GetUser returned an invalid user")
		WriteError(request, ErrInvalidUpstreamResponse)
		return nil, false
	}
	if user.Id != principal.UserID || user.Status != "active" || user.TokenVersion != principal.TokenVersion {
		WriteError(request, ErrAuthenticationRequired)
		return nil, false
	}

	request.Set(principalKey, auth.Principal{
		UserID: user.Id, Role: user.Role, TokenVersion: user.TokenVersion,
	})
	request.Set(identityUserKey, user)
	return user, true
}

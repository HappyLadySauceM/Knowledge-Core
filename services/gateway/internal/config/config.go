package config

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
)

type Config struct {
	App              *option.AppOptions         `mapstructure:"app" json:"app" yaml:"app"`
	Log              *option.LogOptions         `mapstructure:"log" json:"log" yaml:"log"`
	Trace            *option.TraceOptions       `mapstructure:"trace" json:"trace" yaml:"trace"`
	PublicHTTP       *option.HertzServerOptions `mapstructure:"public_http" json:"public_http" yaml:"public_http"`
	AdminHTTP        *option.HertzServerOptions `mapstructure:"admin_http" json:"admin_http" yaml:"admin_http"`
	Redis            *option.RedisOptions       `mapstructure:"redis" json:"redis" yaml:"redis"`
	IdentityRPC      *option.KitexClientOptions `mapstructure:"identity_rpc" json:"identity_rpc" yaml:"identity_rpc"`
	KnowledgeRPC     *option.KitexClientOptions `mapstructure:"knowledge_rpc" json:"knowledge_rpc" yaml:"knowledge_rpc"`
	CollaborationRPC *option.KitexClientOptions `mapstructure:"collaboration_rpc" json:"collaboration_rpc" yaml:"collaboration_rpc"`
	AttachmentRPC    *option.KitexClientOptions `mapstructure:"attachment_rpc" json:"attachment_rpc" yaml:"attachment_rpc"`
	PlatformRPC      *option.KitexClientOptions `mapstructure:"platform_rpc" json:"platform_rpc" yaml:"platform_rpc"`
	Endpoints        *EndpointOptions           `mapstructure:"endpoints" json:"endpoints" yaml:"endpoints"`
	Auth             *AuthOptions               `mapstructure:"auth" json:"auth" yaml:"auth"`
	CORS             *CORSOptions               `mapstructure:"cors" json:"cors" yaml:"cors"`
	RateLimit        *RateLimitOptions          `mapstructure:"rate_limit" json:"rate_limit" yaml:"rate_limit"`
}

func New() Config {
	publicHTTP := option.NewHertzServerOptions()
	publicHTTP.Address = ":8080"
	adminHTTP := option.NewHertzServerOptions()
	adminHTTP.Address = ":8082"
	identityRPC := option.NewKitexClientOptions()
	identityRPC.ServiceName = "knowledge-core.identity"
	identityRPC.Address = "127.0.0.1:8881"
	knowledgeRPC := option.NewKitexClientOptions()
	knowledgeRPC.ServiceName = "knowledge-core.knowledge"
	knowledgeRPC.Address = "127.0.0.1:8882"
	collaborationRPC := option.NewKitexClientOptions()
	collaborationRPC.ServiceName = "knowledge-core.collaboration"
	collaborationRPC.Address = "127.0.0.1:8883"
	collaborationRPC.RequestTimeout = 5 * time.Second
	attachmentRPC := option.NewKitexClientOptions()
	attachmentRPC.ServiceName = "knowledge-core.attachment"
	attachmentRPC.Address = "127.0.0.1:8884"
	attachmentRPC.RequestTimeout = 10 * time.Second
	platformRPC := option.NewKitexClientOptions()
	platformRPC.ServiceName = "knowledge-core.platform"
	platformRPC.Address = "127.0.0.1:8885"
	platformRPC.RequestTimeout = 5 * time.Second
	return Config{
		App: option.NewAppOptions("gateway"), Log: option.NewLogOptions(), Trace: option.NewTraceOptions(),
		PublicHTTP: publicHTTP, AdminHTTP: adminHTTP, Redis: option.NewRedisOptions(),
		IdentityRPC: identityRPC, KnowledgeRPC: knowledgeRPC, CollaborationRPC: collaborationRPC, AttachmentRPC: attachmentRPC, PlatformRPC: platformRPC,
		Endpoints: NewEndpointOptions(), Auth: NewAuthOptions(), CORS: NewCORSOptions(), RateLimit: NewRateLimitOptions(),
	}
}

func (c Config) Validate() error {
	if err := c.requireSections(); err != nil {
		return err
	}
	var addressErr error
	if strings.EqualFold(strings.TrimSpace(c.PublicHTTP.Address), strings.TrimSpace(c.AdminHTTP.Address)) {
		addressErr = errors.New("public_http.address and admin_http.address must be different")
	}
	var shutdownErr error
	drainBudget := c.PublicHTTP.ShutdownTimeout + c.AdminHTTP.ShutdownTimeout
	if c.App.ShutdownTimeout < drainBudget {
		shutdownErr = fmt.Errorf("app.shutdown_timeout must be at least public_http.shutdown_timeout + admin_http.shutdown_timeout (%s)", drainBudget)
	}
	var endpointErr error
	if c.PublicHTTP.TLS.Enabled != strings.HasPrefix(c.Endpoints.PublicBaseURL, "https://") {
		endpointErr = errors.Join(endpointErr, errors.New("endpoints.public_base_url scheme must match public_http TLS"))
	}
	if c.App.Environment != "development" && !strings.HasPrefix(c.Endpoints.CollaborationWebSocketBaseURL, "wss://") {
		endpointErr = errors.Join(endpointErr, errors.New("production collaboration WebSocket URL must use wss"))
	}
	if c.App.Environment == "production" {
		endpointErr = errors.Join(endpointErr, requireMutualTLS("collaboration", c.CollaborationRPC.TLS))
		endpointErr = errors.Join(endpointErr, requireMutualTLS("attachment", c.AttachmentRPC.TLS))
		endpointErr = errors.Join(endpointErr, requireMutualTLS("platform", c.PlatformRPC.TLS))
		endpointErr = errors.Join(endpointErr, option.RejectLoopbackEndpoint("identity_rpc.address", c.IdentityRPC.Address))
		endpointErr = errors.Join(endpointErr, option.RejectLoopbackEndpoint("knowledge_rpc.address", c.KnowledgeRPC.Address))
		endpointErr = errors.Join(endpointErr, option.RejectLoopbackEndpoint("collaboration_rpc.address", c.CollaborationRPC.Address))
		endpointErr = errors.Join(endpointErr, option.RejectLoopbackEndpoint("attachment_rpc.address", c.AttachmentRPC.Address))
		endpointErr = errors.Join(endpointErr, option.RejectLoopbackEndpoint("platform_rpc.address", c.PlatformRPC.Address))
	}
	return errors.Join(
		wrapValidation("app", c.App.Validate()), wrapValidation("log", c.Log.Validate()),
		wrapValidation("trace", c.Trace.Validate()), wrapValidation("public_http", c.PublicHTTP.Validate()),
		wrapValidation("admin_http", c.AdminHTTP.Validate()), wrapValidation("redis", c.Redis.Validate()),
		wrapValidation("identity_rpc", c.IdentityRPC.Validate()),
		wrapValidation("knowledge_rpc", c.KnowledgeRPC.Validate()), wrapValidation("collaboration_rpc", c.CollaborationRPC.Validate()),
		wrapValidation("attachment_rpc", c.AttachmentRPC.Validate()),
		wrapValidation("platform_rpc", c.PlatformRPC.Validate()),
		wrapValidation("endpoints", c.Endpoints.Validate()),
		wrapValidation("auth", c.Auth.Validate()), wrapValidation("cors", c.CORS.Validate()),
		wrapValidation("rate_limit", c.RateLimit.Validate()), addressErr, shutdownErr, endpointErr,
	)
}

func (c Config) requireSections() error {
	sections := map[string]any{
		"app": c.App, "log": c.Log, "trace": c.Trace, "public_http": c.PublicHTTP,
		"admin_http": c.AdminHTTP, "redis": c.Redis,
		"identity_rpc": c.IdentityRPC, "knowledge_rpc": c.KnowledgeRPC, "collaboration_rpc": c.CollaborationRPC, "attachment_rpc": c.AttachmentRPC, "platform_rpc": c.PlatformRPC,
		"endpoints": c.Endpoints, "auth": c.Auth, "cors": c.CORS, "rate_limit": c.RateLimit,
	}
	var joined error
	for name, section := range sections {
		if isNil(section) {
			joined = errors.Join(joined, fmt.Errorf("configuration section %q is required", name))
		}
	}
	return joined
}

func wrapValidation(section string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("validate %s configuration: %w", section, err)
}

func requireMutualTLS(name string, tlsOptions option.TLSOptions) error {
	var joined error
	if !tlsOptions.Enabled || strings.TrimSpace(tlsOptions.CAFile) == "" ||
		strings.TrimSpace(tlsOptions.CertFile) == "" || strings.TrimSpace(tlsOptions.KeyFile) == "" {
		joined = errors.Join(joined, fmt.Errorf("production %s RPC requires a CA and client certificate for mTLS", name))
	}
	if tlsOptions.InsecureSkipVerify {
		joined = errors.Join(joined, fmt.Errorf("production %s RPC TLS verification cannot be disabled", name))
	}
	return joined
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	return reflected.Kind() == reflect.Pointer && reflected.IsNil()
}

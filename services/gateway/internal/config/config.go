package config

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
)

type Config struct {
	App         *option.AppOptions         `mapstructure:"app" json:"app" yaml:"app"`
	Log         *option.LogOptions         `mapstructure:"log" json:"log" yaml:"log"`
	Trace       *option.TraceOptions       `mapstructure:"trace" json:"trace" yaml:"trace"`
	PublicHTTP  *option.HertzServerOptions `mapstructure:"public_http" json:"public_http" yaml:"public_http"`
	AdminHTTP   *option.HertzServerOptions `mapstructure:"admin_http" json:"admin_http" yaml:"admin_http"`
	Redis       *option.RedisOptions       `mapstructure:"redis" json:"redis" yaml:"redis"`
	Etcd        *option.EtcdOptions        `mapstructure:"etcd" json:"etcd" yaml:"etcd"`
	IdentityRPC *option.KitexClientOptions `mapstructure:"identity_rpc" json:"identity_rpc" yaml:"identity_rpc"`
	Auth        *AuthOptions               `mapstructure:"auth" json:"auth" yaml:"auth"`
	CORS        *CORSOptions               `mapstructure:"cors" json:"cors" yaml:"cors"`
	RateLimit   *RateLimitOptions          `mapstructure:"rate_limit" json:"rate_limit" yaml:"rate_limit"`
}

func New() Config {
	publicHTTP := option.NewHertzServerOptions()
	publicHTTP.Address = ":8080"
	adminHTTP := option.NewHertzServerOptions()
	adminHTTP.Address = ":8082"
	identityRPC := option.NewKitexClientOptions()
	identityRPC.ServiceName = "knowledge-core.identity"
	return Config{
		App: option.NewAppOptions("gateway"), Log: option.NewLogOptions(), Trace: option.NewTraceOptions(),
		PublicHTTP: publicHTTP, AdminHTTP: adminHTTP, Redis: option.NewRedisOptions(), Etcd: option.NewEtcdOptions(),
		IdentityRPC: identityRPC, Auth: NewAuthOptions(), CORS: NewCORSOptions(), RateLimit: NewRateLimitOptions(),
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
	return errors.Join(
		wrapValidation("app", c.App.Validate()), wrapValidation("log", c.Log.Validate()),
		wrapValidation("trace", c.Trace.Validate()), wrapValidation("public_http", c.PublicHTTP.Validate()),
		wrapValidation("admin_http", c.AdminHTTP.Validate()), wrapValidation("redis", c.Redis.Validate()),
		wrapValidation("etcd", c.Etcd.Validate()), wrapValidation("identity_rpc", c.IdentityRPC.Validate()),
		wrapValidation("auth", c.Auth.Validate()), wrapValidation("cors", c.CORS.Validate()),
		wrapValidation("rate_limit", c.RateLimit.Validate()), addressErr, shutdownErr,
	)
}

func (c Config) requireSections() error {
	sections := map[string]any{
		"app": c.App, "log": c.Log, "trace": c.Trace, "public_http": c.PublicHTTP,
		"admin_http": c.AdminHTTP, "redis": c.Redis, "etcd": c.Etcd,
		"identity_rpc": c.IdentityRPC, "auth": c.Auth, "cors": c.CORS, "rate_limit": c.RateLimit,
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

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	return reflected.Kind() == reflect.Pointer && reflected.IsNil()
}

package config

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
)

// Config is Identity's complete, explicitly injected application
// configuration. Reusable transport and resource settings live in pkg/option;
// service-specific settings remain in this package.
type Config struct {
	App        *option.AppOptions         `mapstructure:"app" json:"app" yaml:"app"`
	Log        *option.LogOptions         `mapstructure:"log" json:"log" yaml:"log"`
	Trace      *option.TraceOptions       `mapstructure:"trace" json:"trace" yaml:"trace"`
	RPC        *option.KitexServerOptions `mapstructure:"rpc" json:"rpc" yaml:"rpc"`
	HTTP       *option.HertzServerOptions `mapstructure:"http" json:"http" yaml:"http"`
	PostgreSQL *option.PostgreSQLOptions  `mapstructure:"postgres" json:"postgres" yaml:"postgres"`
	Redis      *option.RedisOptions       `mapstructure:"redis" json:"redis" yaml:"redis"`
	Etcd       *option.EtcdOptions        `mapstructure:"etcd" json:"etcd" yaml:"etcd"`
	Bcrypt     *BcryptOptions             `mapstructure:"bcrypt" json:"bcrypt" yaml:"bcrypt"`
	Auth       *AuthOptions               `mapstructure:"auth" json:"auth" yaml:"auth"`
}

func New() Config {
	appOptions := option.NewAppOptions("identity")
	rpcOptions := option.NewKitexServerOptions()
	rpcOptions.ServiceName = "knowledge-core.identity"
	return Config{
		App:        appOptions,
		Log:        option.NewLogOptions(),
		Trace:      option.NewTraceOptions(),
		RPC:        rpcOptions,
		HTTP:       option.NewHertzServerOptions(),
		PostgreSQL: option.NewPostgreSQLOptions(),
		Redis:      option.NewRedisOptions(),
		Etcd:       option.NewEtcdOptions(),
		Bcrypt:     NewBcryptOptions(),
		Auth:       NewAuthOptions(),
	}
}

func (c Config) Validate() error {
	if err := c.requireSections(); err != nil {
		return err
	}
	return errors.Join(
		wrapValidation("app", c.App.Validate()),
		wrapValidation("log", c.Log.Validate()),
		wrapValidation("trace", c.Trace.Validate()),
		wrapValidation("rpc", c.RPC.Validate()),
		wrapValidation("http", c.HTTP.Validate()),
		wrapValidation("postgres", c.PostgreSQL.Validate()),
		wrapValidation("redis", c.Redis.Validate()),
		wrapValidation("etcd", c.Etcd.Validate()),
		wrapValidation("bcrypt", c.Bcrypt.Validate()),
		wrapValidation("auth", c.Auth.Validate()),
		validateLifecycleBudgets(c),
	)
}

func validateLifecycleBudgets(c Config) error {
	var joined error
	// Kitex intentionally waits one second before publishing its registry
	// entry. A shorter process startup budget can never reach the RPC readiness
	// handshake even when every dependency is healthy.
	if c.App.StartupTimeout <= time.Second {
		joined = errors.Join(joined, fmt.Errorf(
			"app.startup_timeout must be greater than the Kitex registration delay of %s",
			time.Second,
		))
	}
	transportDrainBudget := c.RPC.ExitWaitTimeout + c.HTTP.ShutdownTimeout
	if c.App.ShutdownTimeout < transportDrainBudget {
		joined = errors.Join(joined, fmt.Errorf(
			"app.shutdown_timeout must be at least rpc.exit_wait_timeout + http.shutdown_timeout (%s)",
			transportDrainBudget,
		))
	}
	return joined
}

func (c Config) requireSections() error {
	sections := map[string]any{
		"app": c.App, "log": c.Log, "trace": c.Trace, "rpc": c.RPC,
		"http": c.HTTP, "postgres": c.PostgreSQL, "redis": c.Redis,
		"etcd": c.Etcd, "bcrypt": c.Bcrypt,
		"auth": c.Auth,
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

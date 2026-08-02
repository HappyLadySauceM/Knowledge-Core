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
	App           *option.AppOptions         `mapstructure:"app" json:"app" yaml:"app"`
	Log           *option.LogOptions         `mapstructure:"log" json:"log" yaml:"log"`
	Trace         *option.TraceOptions       `mapstructure:"trace" json:"trace" yaml:"trace"`
	RPC           *option.KitexServerOptions `mapstructure:"rpc" json:"rpc" yaml:"rpc"`
	AdminHTTP     *option.HertzServerOptions `mapstructure:"admin_http" json:"admin_http" yaml:"admin_http"`
	InternalHTTP  *option.HertzServerOptions `mapstructure:"internal_http" json:"internal_http" yaml:"internal_http"`
	PostgreSQL    *option.PostgreSQLOptions  `mapstructure:"postgres" json:"postgres" yaml:"postgres"`
	Etcd          *option.EtcdOptions        `mapstructure:"etcd" json:"etcd" yaml:"etcd"`
	NATS          *option.NATSOptions        `mapstructure:"nats" json:"nats" yaml:"nats"`
	IdentityRPC   *option.KitexClientOptions `mapstructure:"identity_rpc" json:"identity_rpc" yaml:"identity_rpc"`
	Auth          *AuthOptions               `mapstructure:"auth" json:"auth" yaml:"auth"`
	ObjectStorage *ObjectStorageOptions      `mapstructure:"object_storage" json:"object_storage" yaml:"object_storage"`
	Scanner       *ScannerOptions            `mapstructure:"scanner" json:"scanner" yaml:"scanner"`
	Collaboration *CollaborationOptions      `mapstructure:"collaboration" json:"collaboration" yaml:"collaboration"`
	Workers       *WorkerOptions             `mapstructure:"workers" json:"workers" yaml:"workers"`
}

func New() Config {
	app := option.NewAppOptions("knowledge")
	app.ShutdownTimeout = 45 * time.Second
	rpc := option.NewKitexServerOptions()
	rpc.Address = ":8882"
	rpc.ServiceName = "knowledge-core.knowledge"
	admin := option.NewHertzServerOptions()
	admin.Address = ":8083"
	internal := option.NewHertzServerOptions()
	internal.Address = ":8090"
	internal.MaxRequestBodySize = 20 << 20
	identity := option.NewKitexClientOptions()
	identity.ServiceName = "knowledge-core.identity"
	natsOptions := option.NewNATSOptions()
	natsOptions.Name = "knowledge-core.knowledge"
	return Config{
		App: app, Log: option.NewLogOptions(), Trace: option.NewTraceOptions(), RPC: rpc,
		AdminHTTP: admin, InternalHTTP: internal, PostgreSQL: option.NewPostgreSQLOptions(),
		Etcd: option.NewEtcdOptions(), NATS: natsOptions, IdentityRPC: identity,
		Auth: NewAuthOptions(), ObjectStorage: NewObjectStorageOptions(), Scanner: NewScannerOptions(),
		Collaboration: NewCollaborationOptions(), Workers: NewWorkerOptions(),
	}
}

func (c Config) Validate() error {
	if err := c.requireSections(); err != nil {
		return err
	}
	var addressErr error
	if c.RPC.Address == c.AdminHTTP.Address || c.RPC.Address == c.InternalHTTP.Address || c.AdminHTTP.Address == c.InternalHTTP.Address {
		addressErr = errors.New("rpc, admin_http, and internal_http addresses must be different")
	}
	var productionErr error
	if c.App.Environment != "development" {
		if !c.InternalHTTP.TLS.Enabled || c.InternalHTTP.TLS.CAFile == "" {
			productionErr = errors.Join(productionErr, errors.New("production internal_http requires mTLS with a client CA"))
		}
		if !strings.HasPrefix(c.Collaboration.BaseURL, "https://") {
			productionErr = errors.Join(productionErr, errors.New("production collaboration.base_url must use https"))
		}
		if c.ObjectStorage.AutoCreateBucket {
			productionErr = errors.Join(productionErr, errors.New("production object storage must not auto-create its bucket"))
		}
	}
	transportBudget := c.RPC.ExitWaitTimeout + c.AdminHTTP.ShutdownTimeout + c.InternalHTTP.ShutdownTimeout
	var lifecycleErr error
	if c.App.StartupTimeout <= time.Second {
		lifecycleErr = errors.New("app.startup_timeout must exceed the Kitex registration delay of 1s")
	}
	if c.App.ShutdownTimeout < transportBudget {
		lifecycleErr = errors.Join(lifecycleErr, fmt.Errorf("app.shutdown_timeout must be at least %s", transportBudget))
	}
	return errors.Join(
		wrapValidation("app", c.App.Validate()), wrapValidation("log", c.Log.Validate()),
		wrapValidation("trace", c.Trace.Validate()), wrapValidation("rpc", c.RPC.Validate()),
		wrapValidation("admin_http", c.AdminHTTP.Validate()), wrapValidation("internal_http", c.InternalHTTP.Validate()),
		wrapValidation("postgres", c.PostgreSQL.Validate()), wrapValidation("etcd", c.Etcd.Validate()),
		wrapValidation("nats", c.NATS.Validate()), wrapValidation("identity_rpc", c.IdentityRPC.Validate()),
		wrapValidation("auth", c.Auth.Validate()), wrapValidation("object_storage", c.ObjectStorage.Validate()),
		wrapValidation("scanner", c.Scanner.Validate()), wrapValidation("collaboration", c.Collaboration.Validate()),
		wrapValidation("workers", c.Workers.Validate()), addressErr, productionErr, lifecycleErr,
	)
}

func (c Config) requireSections() error {
	sections := map[string]any{
		"app": c.App, "log": c.Log, "trace": c.Trace, "rpc": c.RPC,
		"admin_http": c.AdminHTTP, "internal_http": c.InternalHTTP, "postgres": c.PostgreSQL,
		"etcd": c.Etcd, "nats": c.NATS, "identity_rpc": c.IdentityRPC, "auth": c.Auth,
		"object_storage": c.ObjectStorage, "scanner": c.Scanner, "collaboration": c.Collaboration, "workers": c.Workers,
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

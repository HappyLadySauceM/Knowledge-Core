package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/configcenter"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
)

type AuthOptions struct {
	PublicKey     string `mapstructure:"public_key" json:"public_key" yaml:"public_key"`
	InternalToken string `mapstructure:"internal_token" json:"internal_token" yaml:"internal_token"`
}

func (o AuthOptions) Validate() error {
	if strings.TrimSpace(o.PublicKey) == "" || strings.TrimSpace(o.InternalToken) == "" {
		return errors.New("auth.public_key and auth.internal_token are required")
	}
	return nil
}

type EncryptionOptions struct {
	KeyID string `mapstructure:"key_id" json:"key_id" yaml:"key_id"`
	KEK   string `mapstructure:"kek" json:"kek" yaml:"kek"`
}

func (o EncryptionOptions) Validate() error {
	if strings.TrimSpace(o.KeyID) == "" {
		return errors.New("encryption.key_id is required")
	}
	if _, err := configcenter.ParseKey(o.KEK); err != nil {
		return fmt.Errorf("encryption.kek: %w", err)
	}
	return nil
}

type SyncOptions struct {
	Stream       string        `mapstructure:"stream" json:"stream" yaml:"stream"`
	Subject      string        `mapstructure:"subject" json:"subject" yaml:"subject"`
	PollInterval time.Duration `mapstructure:"poll_interval" json:"poll_interval" yaml:"poll_interval"`
	Lease        time.Duration `mapstructure:"lease" json:"lease" yaml:"lease"`
	MaxAttempts  int           `mapstructure:"max_attempts" json:"max_attempts" yaml:"max_attempts"`
}

func (o SyncOptions) Validate() error {
	var joined error
	if strings.TrimSpace(o.Stream) == "" || strings.TrimSpace(o.Subject) == "" {
		joined = errors.Join(joined, errors.New("sync.stream and sync.subject are required"))
	}
	if o.PollInterval < 100*time.Millisecond || o.PollInterval > time.Minute {
		joined = errors.Join(joined, errors.New("sync.poll_interval must be between 100ms and 1m"))
	}
	if o.Lease < time.Second || o.Lease > 5*time.Minute {
		joined = errors.Join(joined, errors.New("sync.lease must be between 1s and 5m"))
	}
	if o.MaxAttempts < 1 || o.MaxAttempts > 32 {
		joined = errors.Join(joined, errors.New("sync.max_attempts must be between 1 and 32"))
	}
	return joined
}

type Config struct {
	App        *option.AppOptions         `mapstructure:"app" json:"app" yaml:"app"`
	Log        *option.LogOptions         `mapstructure:"log" json:"log" yaml:"log"`
	Trace      *option.TraceOptions       `mapstructure:"trace" json:"trace" yaml:"trace"`
	RPC        *option.KitexServerOptions `mapstructure:"rpc" json:"rpc" yaml:"rpc"`
	AdminHTTP  *option.HertzServerOptions `mapstructure:"admin_http" json:"admin_http" yaml:"admin_http"`
	PostgreSQL *option.PostgreSQLOptions  `mapstructure:"postgres" json:"postgres" yaml:"postgres"`
	NATS       *option.NATSOptions        `mapstructure:"nats" json:"nats" yaml:"nats"`
	Auth       *AuthOptions               `mapstructure:"auth" json:"auth" yaml:"auth"`
	Encryption *EncryptionOptions         `mapstructure:"encryption" json:"encryption" yaml:"encryption"`
	Sync       *SyncOptions               `mapstructure:"sync" json:"sync" yaml:"sync"`
}

func New() Config {
	app := option.NewAppOptions("platform")
	app.ShutdownTimeout = 45 * time.Second
	rpc := option.NewKitexServerOptions()
	rpc.Address = ":8885"
	rpc.ServiceName = "knowledge-core.platform"
	admin := option.NewHertzServerOptions()
	admin.Address = ":8086"
	nats := option.NewNATSOptions()
	nats.Name = "knowledge-core.platform"
	return Config{
		App: app, Log: option.NewLogOptions(), Trace: option.NewTraceOptions(), RPC: rpc,
		AdminHTTP: admin, PostgreSQL: option.NewPostgreSQLOptions(), NATS: nats,
		Auth: &AuthOptions{}, Encryption: &EncryptionOptions{},
		Sync: &SyncOptions{Stream: "KNOWLEDGE_CORE_CONFIG", Subject: "platform.config.changed.v1", PollInterval: time.Second, Lease: 30 * time.Second, MaxAttempts: 8},
	}
}

func (c Config) Validate() error {
	if c.App == nil || c.Log == nil || c.Trace == nil || c.RPC == nil || c.AdminHTTP == nil || c.PostgreSQL == nil || c.NATS == nil || c.Auth == nil || c.Encryption == nil || c.Sync == nil {
		return errors.New("all platform configuration sections are required")
	}
	return errors.Join(c.App.Validate(), c.Log.Validate(), c.Trace.Validate(), c.RPC.Validate(), c.AdminHTTP.Validate(), c.PostgreSQL.Validate(), c.NATS.Validate(), c.Auth.Validate(), c.Encryption.Validate(), c.Sync.Validate())
}

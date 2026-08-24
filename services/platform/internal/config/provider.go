package config

import (
	"context"
	"fmt"
	"strings"

	coreapp "github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type Provider struct{ configFile string }

func NewProvider() *Provider { return &Provider{} }

func (p *Provider) AddFlags(flags *pflag.FlagSet) {
	flags.StringVarP(&p.configFile, "config", "c", "", "Path to platform YAML configuration")
	_ = cobra.MarkFlagRequired(flags, "config")
}

func (p *Provider) Load(ctx context.Context, command *cobra.Command) (Config, error) {
	if ctx == nil || command == nil {
		return Config{}, fmt.Errorf("load platform configuration: context and command are required")
	}
	if err := ctx.Err(); err != nil {
		return Config{}, err
	}
	path := strings.TrimSpace(p.configFile)
	if path == "" {
		return Config{}, fmt.Errorf("load platform configuration: --config is required")
	}
	v := viper.New()
	v.SetEnvPrefix("PLATFORM")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	for _, key := range []string{
		"app.name", "app.environment", "app.version", "app.startup_timeout", "app.shutdown_timeout",
		"log.level", "log.add_source", "log.health_check_requests",
		"trace.enabled", "trace.endpoint", "trace.sample_ratio", "trace.insecure", "trace.batch_timeout", "trace.export_timeout", "trace.tls.enabled", "trace.tls.ca_file", "trace.tls.cert_file", "trace.tls.key_file", "trace.tls.server_name", "trace.tls.insecure_skip_verify",
		"rpc.address", "rpc.service_name", "rpc.read_write_timeout", "rpc.exit_wait_timeout", "rpc.max_connections", "rpc.tls.enabled", "rpc.tls.ca_file", "rpc.tls.cert_file", "rpc.tls.key_file", "rpc.tls.server_name", "rpc.tls.insecure_skip_verify",
		"admin_http.address", "admin_http.read_timeout", "admin_http.write_timeout", "admin_http.idle_timeout", "admin_http.shutdown_timeout", "admin_http.max_request_body_size", "admin_http.tls.enabled", "admin_http.tls.ca_file", "admin_http.tls.cert_file", "admin_http.tls.key_file", "admin_http.tls.server_name", "admin_http.tls.insecure_skip_verify",
		"postgres.host", "postgres.port", "postgres.user", "postgres.password", "postgres.database", "postgres.ssl_mode", "postgres.time_zone", "postgres.max_idle_conns", "postgres.max_open_conns", "postgres.conn_max_lifetime", "postgres.conn_max_idle_time", "postgres.connect_timeout", "postgres.slow_threshold", "postgres.prepare_stmt",
		"nats.servers", "nats.name", "nats.username", "nats.password", "nats.token", "nats.credentials_file", "nats.connect_timeout", "nats.request_timeout", "nats.max_reconnects", "nats.reconnect_wait", "nats.ping_interval", "nats.max_pings_out", "nats.drain_timeout", "nats.tls.enabled", "nats.tls.ca_file", "nats.tls.cert_file", "nats.tls.key_file", "nats.tls.server_name", "nats.tls.insecure_skip_verify",
		"auth.public_key", "auth.internal_token", "encryption.key_id", "encryption.kek",
		"sync.stream", "sync.subject", "sync.poll_interval", "sync.lease", "sync.max_attempts",
	} {
		// PLATFORM_INTERNAL_TOKEN is the established deployment Secret key. Keep
		// the structured auth.internal_token field while accepting that stable
		// external name; changing the Secret would require a coordinated rollout.
		if key == "auth.internal_token" {
			if err := v.BindEnv(key, "PLATFORM_INTERNAL_TOKEN"); err != nil {
				return Config{}, fmt.Errorf("bind platform environment %q: %w", key, err)
			}
			continue
		}
		if err := v.BindEnv(key); err != nil {
			return Config{}, fmt.Errorf("bind platform environment %q: %w", key, err)
		}
	}
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("read platform configuration: %w", err)
	}
	loaded := New()
	if err := v.UnmarshalExact(&loaded); err != nil {
		return Config{}, fmt.Errorf("decode platform configuration: %w", err)
	}
	if err := loaded.Validate(); err != nil {
		return Config{}, err
	}
	return loaded, nil
}

func (p *Provider) BindRuntime(context.Context, *coreapp.Runtime) error { return nil }

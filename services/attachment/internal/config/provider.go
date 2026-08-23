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

type Provider struct {
	defaults   Config
	configFile string
	apply      func(Config) error
}

func NewProvider() *Provider { return &Provider{defaults: New()} }
func (p *Provider) AddFlags(flags *pflag.FlagSet) {
	flags.StringVarP(&p.configFile, "config", "c", "", "Path to attachment YAML configuration")
	_ = cobra.MarkFlagRequired(flags, "config")
}
func (p *Provider) Load(ctx context.Context, command *cobra.Command) (Config, error) {
	if ctx == nil || command == nil {
		return Config{}, fmt.Errorf("load attachment configuration: context and command are required")
	}
	if err := ctx.Err(); err != nil {
		return Config{}, err
	}
	path := strings.TrimSpace(p.configFile)
	if path == "" {
		return Config{}, fmt.Errorf("load attachment configuration: --config is required")
	}
	v := viper.New()
	v.SetEnvPrefix("ATTACHMENT")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	for _, key := range []string{
		"app.name", "app.environment", "app.version", "app.startup_timeout", "app.shutdown_timeout",
		"log.level", "log.add_source", "log.health_check_requests",
		"trace.enabled", "trace.endpoint", "trace.sample_ratio", "trace.insecure", "trace.batch_timeout", "trace.export_timeout",
		"rpc.address", "rpc.service_name", "rpc.read_write_timeout", "rpc.exit_wait_timeout", "rpc.max_connections", "rpc.tls.enabled", "rpc.tls.ca_file", "rpc.tls.cert_file", "rpc.tls.key_file", "rpc.tls.server_name", "rpc.tls.insecure_skip_verify",
		"admin_http.address", "admin_http.read_timeout", "admin_http.write_timeout", "admin_http.idle_timeout", "admin_http.shutdown_timeout", "admin_http.max_request_body_size", "admin_http.tls.enabled", "admin_http.tls.ca_file", "admin_http.tls.cert_file", "admin_http.tls.key_file", "admin_http.tls.server_name", "admin_http.tls.insecure_skip_verify",
		"postgres.host", "postgres.port", "postgres.user", "postgres.password", "postgres.database", "postgres.ssl_mode", "postgres.time_zone", "postgres.max_idle_conns", "postgres.max_open_conns", "postgres.conn_max_lifetime", "postgres.conn_max_idle_time", "postgres.connect_timeout", "postgres.slow_threshold", "postgres.prepare_stmt",
		"auth.public_key",
		"object_storage.endpoint", "object_storage.public_endpoint", "object_storage.region", "object_storage.bucket", "object_storage.access_key", "object_storage.secret_key", "object_storage.secure", "object_storage.public_secure", "object_storage.auto_create_bucket", "object_storage.upload_ttl", "object_storage.download_ttl",
		"scanner.address", "scanner.dial_timeout", "scanner.scan_timeout", "scanner.maximum_stream",
	} {
		if err := v.BindEnv(key); err != nil {
			return Config{}, fmt.Errorf("bind attachment environment %q: %w", key, err)
		}
	}
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("read attachment configuration: %w", err)
	}
	loaded := New()
	if err := v.UnmarshalExact(&loaded); err != nil {
		return Config{}, fmt.Errorf("decode attachment configuration: %w", err)
	}
	if err := loaded.Validate(); err != nil {
		return Config{}, err
	}
	return loaded, nil
}
func (p *Provider) BindRuntime(context.Context, *coreapp.Runtime) error { return nil }
func (p *Provider) BindServiceApplier(apply func(Config) error)         { p.apply = apply }

package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	coreapp "github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/configcenter"
	"github.com/bytedance/sonic"
	mapstructure "github.com/go-viper/mapstructure/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

const envPrefix = "GATEWAY"

var stringMapType = reflect.TypeFor[map[string]string]()

type Provider struct {
	defaults   Config
	configFile string
	flagsAdded bool
	remote     *configcenter.Manager
	mu         sync.Mutex
	current    Config
	baseline   Config
	startup    Config
	apply      func(Config) error
}

func NewProvider() *Provider { return &Provider{defaults: New()} }

func (p *Provider) AddFlags(flags *pflag.FlagSet) {
	if flags == nil || p.flagsAdded {
		return
	}
	p.flagsAdded = true
	flags.StringVarP(&p.configFile, "config", "c", "", "Path to the required YAML configuration file")
	_ = cobra.MarkFlagRequired(flags, "config")
}

func (p *Provider) Load(ctx context.Context, command *cobra.Command) (Config, error) {
	if command == nil || ctx == nil {
		return Config{}, errors.New("load gateway configuration: command and context are required")
	}
	if err := ctx.Err(); err != nil {
		return Config{}, fmt.Errorf("load gateway configuration: %w", err)
	}
	configFile := strings.TrimSpace(p.configFile)
	if configFile == "" {
		return Config{}, errors.New("load gateway configuration: --config is required")
	}
	v := viper.New()
	v.SetEnvPrefix(envPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AllowEmptyEnv(true)
	setDefaults(v, reflect.ValueOf(p.defaults), "")
	for _, key := range configurationKeys(reflect.ValueOf(p.defaults), "") {
		if err := v.BindEnv(key); err != nil {
			return Config{}, fmt.Errorf("bind environment variable for %q: %w", key, err)
		}
	}
	if err := readYAMLConfig(v, configFile); err != nil {
		return Config{}, err
	}
	loaded := New()
	if err := v.UnmarshalExact(&loaded, viper.DecodeHook(configurationDecodeHook())); err != nil {
		return Config{}, fmt.Errorf("decode gateway configuration: %w", err)
	}
	remoteBootstrap, err := configcenter.BootstrapFromEnvironment(p.defaults.App.Name)
	if err != nil {
		return Config{}, fmt.Errorf("load gateway dynamic configuration bootstrap: %w", err)
	}
	remote := configcenter.NewManager(remoteBootstrap)
	document, err := remote.Load(ctx)
	if err != nil {
		return Config{}, fmt.Errorf("load gateway dynamic configuration: %w", err)
	}
	baseline := loaded
	if document != nil {
		loaded, _, err = applyDocument(loaded, baseline, loaded, *document)
		if err != nil {
			return Config{}, err
		}
	}
	if err := loaded.Validate(); err != nil {
		return Config{}, err
	}
	p.remote = remote
	p.current = loaded
	p.baseline = baseline
	p.startup = loaded
	return loaded, nil
}

func (p *Provider) BindRuntime(ctx context.Context, runtime *coreapp.Runtime) error {
	if runtime == nil {
		return errors.New("bind gateway runtime configuration: runtime is required")
	}
	if p.remote == nil || !p.remote.Enabled() {
		return nil
	}
	if p.apply == nil {
		return errors.New("bind gateway runtime configuration: service applier is required")
	}
	return p.remote.Start(
		ctx,
		runtime.Logger,
		runtime.Metrics.Registerer(),
		func(document configcenter.DynamicDocument) (configcenter.ApplyResult, error) {
			p.mu.Lock()
			candidate, result, err := applyDocument(p.current, p.baseline, p.startup, document)
			if err != nil {
				p.mu.Unlock()
				return result, err
			}
			if err := runtime.SetLogLevel(candidate.Log.Level); err != nil {
				p.mu.Unlock()
				return result, err
			}
			runtime.SetHealthCheckRequestLogs(candidate.Log.HealthCheckRequests)
			if err := p.apply(candidate); err != nil {
				p.mu.Unlock()
				return result, err
			}
			p.current = candidate
			p.mu.Unlock()
			return result, nil
		},
		runtime.AddCleanup,
	)
}

func (p *Provider) BindServiceApplier(apply func(Config) error) { p.apply = apply }

func applyDocument(current, baseline, startup Config, document configcenter.DynamicDocument) (Config, configcenter.ApplyResult, error) {
	result := configcenter.ApplyResult{}
	candidate, err := cloneConfig(baseline)
	if err != nil {
		return Config{}, result, err
	}
	if err := document.ValidateApplication("gateway", "app.version", "auth.public_key", "redis.username", "redis.password", "trace.headers", "trace.tls.key_file", "public_http.tls.key_file", "admin_http.tls.key_file", "identity_rpc.tls.key_file", "knowledge_rpc.tls.key_file", "collaboration_rpc.tls.key_file", "attachment_rpc.tls.key_file", "platform_rpc.tls.key_file"); err != nil {
		return Config{}, result, err
	}
	if document.Legacy() {
		candidate, err = cloneConfig(current)
		if err != nil {
			return Config{}, result, err
		}
		candidate.Log.Level = document.Log.Level
	} else {
		contents, err := yaml.Marshal(document.Config)
		if err != nil {
			return Config{}, result, fmt.Errorf("encode gateway dynamic configuration: %w", err)
		}
		decoder := yaml.NewDecoder(bytes.NewReader(contents))
		decoder.KnownFields(true)
		if err := decoder.Decode(&candidate); err != nil {
			return Config{}, result, fmt.Errorf("apply gateway dynamic configuration: %w", err)
		}
	}
	candidate, err = applyEnvironmentOverrides(candidate)
	if err != nil {
		return Config{}, result, err
	}
	if err := candidate.Validate(); err != nil {
		return Config{}, result, fmt.Errorf("validate gateway dynamic configuration: %w", err)
	}
	result.RestartRequiredFields = configcenter.RestartRequiredFields(startup, candidate,
		"log.level", "log.health_check_requests", "cors.allowed_origins", "cors.trusted_proxy_cidrs",
		"rate_limit.window", "rate_limit.global_limit", "rate_limit.auth_limit",
		"endpoints.public_base_url", "endpoints.collaboration_websocket_base_url")
	return candidate, result, nil
}

func cloneConfig(source Config) (Config, error) {
	contents, err := yaml.Marshal(source)
	if err != nil {
		return Config{}, fmt.Errorf("clone gateway configuration: %w", err)
	}
	clone := New()
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(&clone); err != nil {
		return Config{}, fmt.Errorf("clone gateway configuration: %w", err)
	}
	return clone, nil
}

func applyEnvironmentOverrides(base Config) (Config, error) {
	v := viper.New()
	v.SetEnvPrefix(envPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AllowEmptyEnv(true)
	setDefaults(v, reflect.ValueOf(base), "")
	for _, key := range configurationKeys(reflect.ValueOf(base), "") {
		if err := v.BindEnv(key); err != nil {
			return Config{}, fmt.Errorf("bind gateway environment variable for %q: %w", key, err)
		}
	}
	loaded := New()
	if err := v.UnmarshalExact(&loaded, viper.DecodeHook(configurationDecodeHook())); err != nil {
		return Config{}, fmt.Errorf("decode gateway environment overrides: %w", err)
	}
	return loaded, nil
}

func readYAMLConfig(v *viper.Viper, path string) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
	default:
		return fmt.Errorf("read gateway configuration %q: file extension must be .yaml or .yml", path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read gateway configuration %q: %w", path, err)
	}
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(contents)); err != nil {
		return fmt.Errorf("parse gateway configuration %q: %w", path, err)
	}
	return nil
}

func configurationDecodeHook() mapstructure.DecodeHookFunc {
	return mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
		stringToStringMapHook(),
	)
}

func stringToStringMapHook() mapstructure.DecodeHookFuncType {
	return func(from, to reflect.Type, data any) (any, error) {
		if from.Kind() != reflect.String || to != stringMapType {
			return data, nil
		}
		raw := strings.TrimSpace(data.(string))
		if raw == "" {
			return map[string]string{}, nil
		}
		decoded := make(map[string]string)
		if err := sonic.Unmarshal([]byte(raw), &decoded); err != nil {
			return nil, fmt.Errorf("decode JSON string map: %w", err)
		}
		return decoded, nil
	}
}

func setDefaults(v *viper.Viper, value reflect.Value, prefix string) {
	value = indirect(value)
	if !value.IsValid() {
		return
	}
	if value.Type() == reflect.TypeFor[time.Duration]() || value.Kind() != reflect.Struct {
		if prefix != "" {
			v.SetDefault(prefix, value.Interface())
		}
		return
	}
	for index := 0; index < value.NumField(); index++ {
		fieldInfo := value.Type().Field(index)
		name := mapstructureName(fieldInfo)
		if name != "" {
			setDefaults(v, value.Field(index), joinKey(prefix, name))
		}
	}
}

func configurationKeys(value reflect.Value, prefix string) []string {
	value = indirect(value)
	if !value.IsValid() {
		return nil
	}
	if value.Type() == reflect.TypeFor[time.Duration]() || value.Kind() != reflect.Struct {
		if prefix == "" {
			return nil
		}
		return []string{prefix}
	}
	var keys []string
	for index := 0; index < value.NumField(); index++ {
		fieldInfo := value.Type().Field(index)
		name := mapstructureName(fieldInfo)
		if name != "" {
			keys = append(keys, configurationKeys(value.Field(index), joinKey(prefix, name))...)
		}
	}
	return keys
}

func indirect(value reflect.Value) reflect.Value {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return reflect.Value{}
		}
		value = value.Elem()
	}
	return value
}

func mapstructureName(field reflect.StructField) string {
	if field.PkgPath != "" {
		return ""
	}
	name := strings.Split(field.Tag.Get("mapstructure"), ",")[0]
	if name == "-" {
		return ""
	}
	if name == "" {
		name = strings.ToLower(field.Name)
	}
	return name
}

func joinKey(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

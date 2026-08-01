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
	"time"

	"github.com/bytedance/sonic"
	mapstructure "github.com/go-viper/mapstructure/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const envPrefix = "GATEWAY"

var stringMapType = reflect.TypeFor[map[string]string]()

type Provider struct {
	defaults   Config
	configFile string
	flagsAdded bool
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
	if err := loaded.Validate(); err != nil {
		return Config{}, err
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

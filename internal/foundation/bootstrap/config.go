package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/cache/redis"
	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/database"
	discoveryetcd "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/discovery/etcd"
	natsadapter "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/messaging/nats"
)

type Needs struct {
	Database          bool
	Cache             bool
	DurableMessaging  bool
	RealtimeMessaging bool
	ConfigSource      bool
	Registry          bool
	Resolver          bool
}

type Config struct {
	Environment     string
	Service         string
	ListenAddress   string
	LogLevel        string
	ShutdownTimeout time.Duration

	DatabaseProvider string
	Database         database.Config

	CacheProvider string
	Redis         redis.Config

	DurableMessagingProvider  string
	RealtimeMessagingProvider string
	NATS                      natsadapter.Config

	ConfigProvider string
	Etcd           discoveryetcd.Config
}

type LookupFunc func(string) (string, bool)

func LoadConfig(service, listenVariable, defaultAddress string, needs Needs) (Config, error) {
	return loadConfig(service, listenVariable, defaultAddress, needs, os.LookupEnv)
}

func loadConfig(service, listenVariable, defaultAddress string, needs Needs, lookup LookupFunc) (Config, error) {
	config := Config{
		Environment:               valueOrDefault(lookup, "KC_ENV", "local"),
		Service:                   service,
		ListenAddress:             valueOrDefault(lookup, listenVariable, defaultAddress),
		LogLevel:                  valueOrDefault(lookup, "KC_LOG_LEVEL", "info"),
		DatabaseProvider:          valueOrDefault(lookup, "KC_DATABASE_PROVIDER", "postgres"),
		CacheProvider:             valueOrDefault(lookup, "KC_CACHE_PROVIDER", "redis"),
		DurableMessagingProvider:  valueOrDefault(lookup, "KC_MESSAGING_DURABLE_PROVIDER", "nats"),
		RealtimeMessagingProvider: valueOrDefault(lookup, "KC_MESSAGING_REALTIME_PROVIDER", "nats"),
		ConfigProvider:            valueOrDefault(lookup, "KC_CONFIG_PROVIDER", "etcd"),
	}

	var err error
	if config.ShutdownTimeout, err = durationValue(lookup, "KC_SHUTDOWN_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if config.Database.MaxOpenConns, err = intValue(lookup, "KC_DATABASE_MAX_OPEN_CONNS", 20); err != nil {
		return Config{}, err
	}
	if config.Database.MaxIdleConns, err = intValue(lookup, "KC_DATABASE_MAX_IDLE_CONNS", 5); err != nil {
		return Config{}, err
	}
	if config.Database.ConnMaxLifetime, err = durationValue(lookup, "KC_DATABASE_CONN_MAX_LIFETIME", 30*time.Minute); err != nil {
		return Config{}, err
	}
	if config.Database.ConnMaxIdleTime, err = durationValue(lookup, "KC_DATABASE_CONN_MAX_IDLE_TIME", 5*time.Minute); err != nil {
		return Config{}, err
	}
	config.Database.DSN, _ = lookup("KC_DATABASE_DSN")

	config.Redis.Address = valueOrDefault(lookup, "KC_CACHE_ENDPOINTS", "127.0.0.1:6379")
	config.Redis.Username, _ = lookup("KC_REDIS_USERNAME")
	config.Redis.Password, _ = lookup("KC_REDIS_PASSWORD")
	if config.Redis.DB, err = intValue(lookup, "KC_REDIS_DB", 0); err != nil {
		return Config{}, err
	}
	if config.Redis.DialTimeout, err = durationValue(lookup, "KC_REDIS_DIAL_TIMEOUT", 3*time.Second); err != nil {
		return Config{}, err
	}
	config.Redis.ReadTimeout = 2 * time.Second
	config.Redis.WriteTimeout = 2 * time.Second

	config.NATS.URL = valueOrDefault(lookup, "KC_NATS_URL", "nats://127.0.0.1:4222")
	config.NATS.Name = "knowledge-core-" + service
	if config.NATS.ConnectTimeout, err = durationValue(lookup, "KC_NATS_CONNECT_TIMEOUT", 3*time.Second); err != nil {
		return Config{}, err
	}
	if config.NATS.MaxReconnects, err = intValue(lookup, "KC_NATS_MAX_RECONNECTS", 10); err != nil {
		return Config{}, err
	}
	config.NATS.ReconnectWait = time.Second

	config.Etcd.Endpoints = splitNonEmpty(valueOrDefault(lookup, "KC_ETCD_ENDPOINTS", "127.0.0.1:2379"))
	config.Etcd.Prefix = fmt.Sprintf("/knowledge-core/%s/registry", config.Environment)
	config.Etcd.Username, _ = lookup("KC_ETCD_USERNAME")
	config.Etcd.Password, _ = lookup("KC_ETCD_PASSWORD")
	if config.Etcd.DialTimeout, err = durationValue(lookup, "KC_ETCD_DIAL_TIMEOUT", 3*time.Second); err != nil {
		return Config{}, err
	}

	if err := config.Validate(needs); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c Config) Validate(needs Needs) error {
	switch {
	case c.Environment == "":
		return errors.New("validate bootstrap config: environment is required")
	case c.Service == "":
		return errors.New("validate bootstrap config: service is required")
	case c.ListenAddress == "":
		return errors.New("validate bootstrap config: listen address is required")
	case needs.Database && c.DatabaseProvider != "postgres":
		return fmt.Errorf("validate bootstrap config: unsupported database provider %q", c.DatabaseProvider)
	case needs.Database && c.Database.DSN == "":
		return errors.New("validate bootstrap config: KC_DATABASE_DSN is required")
	case needs.Cache && c.CacheProvider != "redis":
		return fmt.Errorf("validate bootstrap config: unsupported cache provider %q", c.CacheProvider)
	case needs.DurableMessaging && c.DurableMessagingProvider != "nats":
		return fmt.Errorf("validate bootstrap config: unsupported durable messaging provider %q", c.DurableMessagingProvider)
	case needs.RealtimeMessaging && c.RealtimeMessagingProvider != "nats":
		return fmt.Errorf("validate bootstrap config: unsupported realtime messaging provider %q", c.RealtimeMessagingProvider)
	case (needs.ConfigSource || needs.Registry || needs.Resolver) && c.ConfigProvider != "etcd":
		return fmt.Errorf("validate bootstrap config: unsupported config/discovery provider %q", c.ConfigProvider)
	case c.ShutdownTimeout <= 0:
		return errors.New("validate bootstrap config: shutdown timeout must be positive")
	default:
		return nil
	}
}

func valueOrDefault(lookup LookupFunc, key, fallback string) string {
	if value, exists := lookup(key); exists && value != "" {
		return value
	}
	return fallback
}

func durationValue(lookup LookupFunc, key string, fallback time.Duration) (time.Duration, error) {
	value, exists := lookup(key)
	if !exists || value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func intValue(lookup LookupFunc, key string, fallback int) (int, error) {
	value, exists := lookup(key)
	if !exists || value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func splitNonEmpty(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

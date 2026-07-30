package middleware

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRateLimitWindow = time.Minute
	defaultGlobalLimit     = int64(300)
	defaultAuthLimit       = int64(20)
)

type LookupFunc func(string) (string, bool)

type Config struct {
	TrustedProxyCIDRs []*net.IPNet
	CORS              CORSConfig
	RateLimit         RateLimitConfig
}

type CORSConfig struct {
	AllowedOrigins []string
}

type RateLimitConfig struct {
	Window      time.Duration
	GlobalLimit int64
	AuthLimit   int64
	now         func() time.Time
}

func LoadConfig(lookup LookupFunc) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("load gateway middleware config: environment lookup is required")
	}

	trustedCIDRs, err := parseTrustedProxyCIDRs(value(lookup, "KC_GATEWAY_TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return Config{}, err
	}
	allowedOrigins, err := parseAllowedOrigins(value(lookup, "KC_GATEWAY_CORS_ALLOWED_ORIGINS"))
	if err != nil {
		return Config{}, err
	}
	window, err := durationSetting(lookup, "KC_GATEWAY_RATE_LIMIT_WINDOW", defaultRateLimitWindow)
	if err != nil {
		return Config{}, err
	}
	globalLimit, err := positiveInt64Setting(lookup, "KC_GATEWAY_RATE_LIMIT_GLOBAL", defaultGlobalLimit)
	if err != nil {
		return Config{}, err
	}
	authLimit, err := positiveInt64Setting(lookup, "KC_GATEWAY_RATE_LIMIT_AUTH", defaultAuthLimit)
	if err != nil {
		return Config{}, err
	}

	return Config{
		TrustedProxyCIDRs: trustedCIDRs,
		CORS:              CORSConfig{AllowedOrigins: allowedOrigins},
		RateLimit: RateLimitConfig{
			Window: window, GlobalLimit: globalLimit, AuthLimit: authLimit,
		},
	}, nil
}

func (c Config) Validate() error {
	for _, network := range c.TrustedProxyCIDRs {
		if network == nil {
			return errors.New("validate gateway middleware config: trusted proxy CIDR must not be nil")
		}
	}
	if _, err := parseAllowedOrigins(strings.Join(c.CORS.AllowedOrigins, ",")); err != nil {
		return fmt.Errorf("validate gateway middleware config: %w", err)
	}
	if c.RateLimit.Window <= 0 {
		return errors.New("validate gateway middleware config: rate limit window must be positive")
	}
	if c.RateLimit.GlobalLimit <= 0 {
		return errors.New("validate gateway middleware config: global rate limit must be positive")
	}
	if c.RateLimit.AuthLimit <= 0 {
		return errors.New("validate gateway middleware config: auth rate limit must be positive")
	}
	return nil
}

func parseTrustedProxyCIDRs(raw string) ([]*net.IPNet, error) {
	values := splitCommaSeparated(raw)
	cidrs := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("parse KC_GATEWAY_TRUSTED_PROXY_CIDRS value %q: %w", value, err)
		}
		cidrs = append(cidrs, network)
	}
	return cidrs, nil
}

func parseAllowedOrigins(raw string) ([]string, error) {
	values := splitCommaSeparated(raw)
	origins := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, origin := range values {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
			parsed.Opaque != "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") || strings.Contains(origin, "*") {
			return nil, fmt.Errorf("parse KC_GATEWAY_CORS_ALLOWED_ORIGINS: %q is not an exact HTTP origin", origin)
		}
		if _, exists := seen[origin]; exists {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	return origins, nil
}

func durationSetting(lookup LookupFunc, key string, fallback time.Duration) (time.Duration, error) {
	raw := value(lookup, key)
	if raw == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("parse %s: duration must be positive", key)
	}
	return parsed, nil
}

func positiveInt64Setting(lookup LookupFunc, key string, fallback int64) (int64, error) {
	raw := value(lookup, key)
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("parse %s: value must be positive", key)
	}
	return parsed, nil
}

func value(lookup LookupFunc, key string) string {
	raw, _ := lookup(key)
	return strings.TrimSpace(raw)
}

func splitCommaSeparated(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			values = append(values, part)
		}
	}
	return values
}

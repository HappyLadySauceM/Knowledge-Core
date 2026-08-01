package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
)

type AuthOptions struct {
	PublicKey string `mapstructure:"public_key" json:"public_key" yaml:"public_key"`
}

func NewAuthOptions() *AuthOptions { return &AuthOptions{} }

func (o AuthOptions) Validate() error {
	_, err := coreauth.NewVerifier(o.PublicKey)
	return err
}

type CORSOptions struct {
	AllowedOrigins    []string `mapstructure:"allowed_origins" json:"allowed_origins" yaml:"allowed_origins"`
	TrustedProxyCIDRs []string `mapstructure:"trusted_proxy_cidrs" json:"trusted_proxy_cidrs" yaml:"trusted_proxy_cidrs"`
}

func NewCORSOptions() *CORSOptions { return &CORSOptions{} }

func (o CORSOptions) Validate() error {
	var joined error
	seen := make(map[string]struct{}, len(o.AllowedOrigins))
	for index, origin := range o.AllowedOrigins {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
			parsed.Opaque != "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") || strings.Contains(origin, "*") {
			joined = errors.Join(joined, fmt.Errorf("allowed_origins[%d] must be an exact HTTP origin", index))
			continue
		}
		if _, duplicate := seen[origin]; duplicate {
			joined = errors.Join(joined, fmt.Errorf("allowed_origins[%d] duplicates %q", index, origin))
		}
		seen[origin] = struct{}{}
	}
	for index, cidr := range o.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(strings.TrimSpace(cidr)); err != nil {
			joined = errors.Join(joined, fmt.Errorf("trusted_proxy_cidrs[%d]: %w", index, err))
		}
	}
	return joined
}

func (o CORSOptions) ParsedTrustedProxyCIDRs() ([]*net.IPNet, error) {
	result := make([]*net.IPNet, 0, len(o.TrustedProxyCIDRs))
	for _, raw := range o.TrustedProxyCIDRs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err != nil {
			return nil, err
		}
		result = append(result, network)
	}
	return result, nil
}

type RateLimitOptions struct {
	Window      time.Duration `mapstructure:"window" json:"window" yaml:"window"`
	GlobalLimit int64         `mapstructure:"global_limit" json:"global_limit" yaml:"global_limit"`
	AuthLimit   int64         `mapstructure:"auth_limit" json:"auth_limit" yaml:"auth_limit"`
}

func NewRateLimitOptions() *RateLimitOptions {
	return &RateLimitOptions{Window: time.Minute, GlobalLimit: 300, AuthLimit: 20}
}

func (o RateLimitOptions) Validate() error {
	var joined error
	if o.Window < time.Millisecond {
		joined = errors.Join(joined, errors.New("window must be at least one millisecond"))
	}
	if o.GlobalLimit <= 0 {
		joined = errors.Join(joined, errors.New("global_limit must be positive"))
	}
	if o.AuthLimit <= 0 {
		joined = errors.Join(joined, errors.New("auth_limit must be positive"))
	}
	return joined
}

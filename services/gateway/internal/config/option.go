package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
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

type CollaborationOptions struct {
	BaseURL        string            `mapstructure:"base_url" json:"base_url" yaml:"base_url"`
	RequestTimeout time.Duration     `mapstructure:"request_timeout" json:"request_timeout" yaml:"request_timeout"`
	TLS            option.TLSOptions `mapstructure:"tls" json:"tls" yaml:"tls"`
}

func NewCollaborationOptions() *CollaborationOptions {
	return &CollaborationOptions{BaseURL: "http://127.0.0.1:8092", RequestTimeout: 5 * time.Second}
}

func (o CollaborationOptions) Validate() error {
	parsed, err := url.Parse(o.BaseURL)
	var endpointErr error
	if err != nil || parsed == nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		endpointErr = errors.New("collaboration.base_url must be an absolute HTTP origin without credentials")
	} else if (parsed.Scheme == "https") != o.TLS.Enabled {
		endpointErr = errors.New("collaboration TLS settings must match collaboration.base_url")
	}
	var timeoutErr error
	if o.RequestTimeout <= 0 {
		timeoutErr = errors.New("collaboration.request_timeout must be positive")
	}
	return errors.Join(endpointErr, timeoutErr, o.TLS.Validate())
}

func (o CollaborationOptions) ValidateProduction() error {
	parsed, err := url.Parse(o.BaseURL)
	if err != nil || parsed == nil || parsed.Scheme != "https" {
		return errors.New("production Collaboration traffic must use HTTPS")
	}
	var joined error
	if !o.TLS.Enabled || strings.TrimSpace(o.TLS.CAFile) == "" || strings.TrimSpace(o.TLS.CertFile) == "" || strings.TrimSpace(o.TLS.KeyFile) == "" {
		joined = errors.Join(joined, errors.New("production Collaboration traffic requires a CA and client certificate for mTLS"))
	}
	if o.TLS.InsecureSkipVerify {
		joined = errors.Join(joined, errors.New("production Collaboration TLS verification cannot be disabled"))
	}
	return joined
}

type EndpointOptions struct {
	PublicBaseURL             string `mapstructure:"public_base_url" json:"public_base_url" yaml:"public_base_url"`
	CollaborationWebSocketURL string `mapstructure:"collaboration_websocket_url" json:"collaboration_websocket_url" yaml:"collaboration_websocket_url"`
}

func NewEndpointOptions() *EndpointOptions {
	return &EndpointOptions{
		PublicBaseURL: "http://localhost:8080", CollaborationWebSocketURL: "ws://localhost:8091/collaboration",
	}
}

func (o EndpointOptions) Validate() error {
	public, publicErr := url.Parse(o.PublicBaseURL)
	if publicErr != nil || !validOrigin(public, "http", "https") {
		publicErr = errors.New("endpoints.public_base_url must be an absolute HTTP origin without credentials")
	}
	websocket, websocketErr := url.Parse(o.CollaborationWebSocketURL)
	if websocketErr != nil || websocket == nil || websocket.Host == "" || websocket.Hostname() == "" || websocket.User != nil ||
		websocket.RawQuery != "" || websocket.Fragment != "" || websocket.Path == "" || websocket.Path == "/" ||
		(websocket.Scheme != "ws" && websocket.Scheme != "wss") {
		websocketErr = errors.New("endpoints.collaboration_websocket_url must be an absolute ws/wss URL with a path")
	}
	return errors.Join(publicErr, websocketErr)
}

func validOrigin(parsed *url.URL, schemes ...string) bool {
	if parsed == nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	for _, scheme := range schemes {
		if parsed.Scheme == scheme {
			return true
		}
	}
	return false
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

package option

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

type EtcdOptions struct {
	Endpoints      []string      `mapstructure:"endpoints" json:"endpoints" yaml:"endpoints"`
	Prefix         string        `mapstructure:"prefix" json:"prefix" yaml:"prefix"`
	Username       string        `mapstructure:"username" json:"username" yaml:"username"`
	Password       string        `mapstructure:"password" json:"password" yaml:"password"`
	DialTimeout    time.Duration `mapstructure:"dial_timeout" json:"dial_timeout" yaml:"dial_timeout"`
	RequestTimeout time.Duration `mapstructure:"request_timeout" json:"request_timeout" yaml:"request_timeout"`
	TLS            TLSOptions    `mapstructure:"tls" json:"tls" yaml:"tls"`
}

func NewEtcdOptions() *EtcdOptions {
	return &EtcdOptions{
		Endpoints:      []string{"127.0.0.1:2379"},
		Prefix:         "/knowledge-core/development/registry",
		DialTimeout:    5 * time.Second,
		RequestTimeout: 3 * time.Second,
	}
}

func (o EtcdOptions) Validate() error {
	var endpointsErr error
	var transportErr error
	if len(o.Endpoints) == 0 {
		endpointsErr = fmt.Errorf("etcd.endpoints requires at least one endpoint")
	}
	for index, endpoint := range o.Endpoints {
		if err := validateEtcdEndpoint(endpoint); err != nil {
			endpointsErr = join(endpointsErr, fmt.Errorf("etcd.endpoints[%d]: %w", index, err))
		}
		if strings.HasPrefix(endpoint, "https://") && !o.TLS.Enabled {
			transportErr = join(transportErr, fmt.Errorf("etcd.endpoints[%d] uses https but etcd.tls.enabled is false", index))
		}
	}
	prefix := strings.TrimSpace(o.Prefix)
	var prefixErr error
	if prefix == "" || prefix == "/" || !strings.HasPrefix(prefix, "/") {
		prefixErr = fmt.Errorf("etcd.prefix must be an absolute, non-root key prefix, got %q", o.Prefix)
	}
	var authErr error
	if (o.Username == "") != (o.Password == "") {
		authErr = fmt.Errorf("etcd.username and etcd.password must be configured together")
	}
	return join(
		endpointsErr,
		transportErr,
		prefixErr,
		authErr,
		positiveDuration("etcd.dial_timeout", o.DialTimeout),
		positiveDuration("etcd.request_timeout", o.RequestTimeout),
		o.TLS.Validate(),
	)
}

func validateEtcdEndpoint(endpoint string) error {
	if strings.Contains(endpoint, "://") {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return err
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return fmt.Errorf("scheme must be http or https")
		}
		if parsed.User != nil {
			return fmt.Errorf("embedded endpoint credentials are forbidden")
		}
		if parsed.Path != "" && parsed.Path != "/" {
			return fmt.Errorf("endpoint path is not supported")
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("endpoint query and fragment are not supported")
		}
		return validateEndpoint("endpoint", parsed.Host)
	}
	return validateEndpoint("endpoint", endpoint)
}

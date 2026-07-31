package option

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

type NATSOptions struct {
	Servers         []string      `mapstructure:"servers" json:"servers" yaml:"servers"`
	Name            string        `mapstructure:"name" json:"name" yaml:"name"`
	Username        string        `mapstructure:"username" json:"username" yaml:"username"`
	Password        string        `mapstructure:"password" json:"password" yaml:"password"`
	Token           string        `mapstructure:"token" json:"token" yaml:"token"`
	CredentialsFile string        `mapstructure:"credentials_file" json:"credentials_file" yaml:"credentials_file"`
	ConnectTimeout  time.Duration `mapstructure:"connect_timeout" json:"connect_timeout" yaml:"connect_timeout"`
	RequestTimeout  time.Duration `mapstructure:"request_timeout" json:"request_timeout" yaml:"request_timeout"`
	MaxReconnects   int           `mapstructure:"max_reconnects" json:"max_reconnects" yaml:"max_reconnects"`
	ReconnectWait   time.Duration `mapstructure:"reconnect_wait" json:"reconnect_wait" yaml:"reconnect_wait"`
	PingInterval    time.Duration `mapstructure:"ping_interval" json:"ping_interval" yaml:"ping_interval"`
	MaxPingsOut     int           `mapstructure:"max_pings_out" json:"max_pings_out" yaml:"max_pings_out"`
	DrainTimeout    time.Duration `mapstructure:"drain_timeout" json:"drain_timeout" yaml:"drain_timeout"`
	TLS             TLSOptions    `mapstructure:"tls" json:"tls" yaml:"tls"`
}

func NewNATSOptions() *NATSOptions {
	return &NATSOptions{
		Servers:        []string{"nats://127.0.0.1:4222"},
		Name:           "knowledge-core",
		ConnectTimeout: 5 * time.Second,
		RequestTimeout: 5 * time.Second,
		MaxReconnects:  -1,
		ReconnectWait:  2 * time.Second,
		PingInterval:   2 * time.Minute,
		MaxPingsOut:    2,
		DrainTimeout:   10 * time.Second,
	}
}

func (o NATSOptions) Validate() error {
	var serversErr error
	if len(o.Servers) == 0 {
		serversErr = fmt.Errorf("nats.servers requires at least one server")
	}
	for index, server := range o.Servers {
		parsed, err := url.Parse(server)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "nats" && parsed.Scheme != "tls") {
			serversErr = join(serversErr, fmt.Errorf("nats.servers[%d] must be a nats:// or tls:// URL", index))
			continue
		}
		if parsed.User != nil {
			serversErr = join(serversErr, fmt.Errorf("nats.servers[%d] must not contain credentials", index))
		}
		if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			serversErr = join(serversErr, fmt.Errorf("nats.servers[%d] must not contain a path, query, or fragment", index))
		}
		if endpointErr := validateEndpoint("server", parsed.Host); endpointErr != nil {
			serversErr = join(serversErr, fmt.Errorf("nats.servers[%d]: %w", index, endpointErr))
		}
	}
	methods := 0
	if o.Token != "" {
		methods++
	}
	if o.CredentialsFile != "" {
		methods++
	}
	if o.Username != "" || o.Password != "" {
		methods++
	}
	var authErr error
	if (o.Username == "") != (o.Password == "") {
		authErr = join(authErr, fmt.Errorf("nats.username and nats.password must be configured together"))
	}
	if methods > 1 {
		authErr = join(authErr, fmt.Errorf("nats token, credentials file, and username/password authentication are mutually exclusive"))
	}
	var reconnectErr error
	if o.MaxReconnects < -1 {
		reconnectErr = newValueError("nats.max_reconnects", ">= -1", o.MaxReconnects)
	}
	var pingsErr error
	if o.MaxPingsOut <= 0 {
		pingsErr = newValueError("nats.max_pings_out", "> 0", o.MaxPingsOut)
	}
	var schemeTLSErr error
	for _, server := range o.Servers {
		if strings.HasPrefix(server, "tls://") && !o.TLS.Enabled {
			schemeTLSErr = fmt.Errorf("nats tls:// servers require nats.tls.enabled=true")
			break
		}
	}
	return join(
		serversErr,
		require("nats.name", o.Name),
		authErr,
		positiveDuration("nats.connect_timeout", o.ConnectTimeout),
		positiveDuration("nats.request_timeout", o.RequestTimeout),
		reconnectErr,
		positiveDuration("nats.reconnect_wait", o.ReconnectWait),
		positiveDuration("nats.ping_interval", o.PingInterval),
		pingsErr,
		positiveDuration("nats.drain_timeout", o.DrainTimeout),
		schemeTLSErr,
		o.TLS.Validate(),
	)
}

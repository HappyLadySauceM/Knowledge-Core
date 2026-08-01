package option

import "time"

type KitexServerOptions struct {
	Address          string        `mapstructure:"address" json:"address" yaml:"address"`
	ServiceName      string        `mapstructure:"service_name" json:"service_name" yaml:"service_name"`
	ReadWriteTimeout time.Duration `mapstructure:"read_write_timeout" json:"read_write_timeout" yaml:"read_write_timeout"`
	ExitWaitTimeout  time.Duration `mapstructure:"exit_wait_timeout" json:"exit_wait_timeout" yaml:"exit_wait_timeout"`
	MaxConnections   int           `mapstructure:"max_connections" json:"max_connections" yaml:"max_connections"`
	TLS              TLSOptions    `mapstructure:"tls" json:"tls" yaml:"tls"`
}

type KitexClientOptions struct {
	ServiceName    string        `mapstructure:"service_name" json:"service_name" yaml:"service_name"`
	ConnectTimeout time.Duration `mapstructure:"connect_timeout" json:"connect_timeout" yaml:"connect_timeout"`
	RequestTimeout time.Duration `mapstructure:"request_timeout" json:"request_timeout" yaml:"request_timeout"`
	TLS            TLSOptions    `mapstructure:"tls" json:"tls" yaml:"tls"`
}

func NewKitexClientOptions() *KitexClientOptions {
	return &KitexClientOptions{
		ServiceName:    "knowledge-core.service",
		ConnectTimeout: 500 * time.Millisecond,
		RequestTimeout: 3 * time.Second,
	}
}

func (o KitexClientOptions) Validate() error {
	return join(
		require("rpc_client.service_name", o.ServiceName),
		positiveDuration("rpc_client.connect_timeout", o.ConnectTimeout),
		positiveDuration("rpc_client.request_timeout", o.RequestTimeout),
		o.TLS.Validate(),
	)
}

func NewKitexServerOptions() *KitexServerOptions {
	return &KitexServerOptions{
		Address:          ":8881",
		ServiceName:      "knowledge-core.service",
		ReadWriteTimeout: 30 * time.Second,
		ExitWaitTimeout:  10 * time.Second,
	}
}

func (o KitexServerOptions) Validate() error {
	var maxConnectionsErr error
	if o.MaxConnections < 0 {
		maxConnectionsErr = newValueError("rpc.max_connections", ">= 0", o.MaxConnections)
	}
	return join(
		validateListenAddress("rpc.address", o.Address),
		require("rpc.service_name", o.ServiceName),
		positiveDuration("rpc.read_write_timeout", o.ReadWriteTimeout),
		positiveDuration("rpc.exit_wait_timeout", o.ExitWaitTimeout),
		maxConnectionsErr,
		o.TLS.Validate(),
	)
}

type HertzServerOptions struct {
	Address            string        `mapstructure:"address" json:"address" yaml:"address"`
	ReadTimeout        time.Duration `mapstructure:"read_timeout" json:"read_timeout" yaml:"read_timeout"`
	WriteTimeout       time.Duration `mapstructure:"write_timeout" json:"write_timeout" yaml:"write_timeout"`
	IdleTimeout        time.Duration `mapstructure:"idle_timeout" json:"idle_timeout" yaml:"idle_timeout"`
	ShutdownTimeout    time.Duration `mapstructure:"shutdown_timeout" json:"shutdown_timeout" yaml:"shutdown_timeout"`
	MaxRequestBodySize int           `mapstructure:"max_request_body_size" json:"max_request_body_size" yaml:"max_request_body_size"`
	TLS                TLSOptions    `mapstructure:"tls" json:"tls" yaml:"tls"`
}

func NewHertzServerOptions() *HertzServerOptions {
	return &HertzServerOptions{
		Address:            ":8081",
		ReadTimeout:        10 * time.Second,
		WriteTimeout:       30 * time.Second,
		IdleTimeout:        2 * time.Minute,
		ShutdownTimeout:    15 * time.Second,
		MaxRequestBodySize: 4 << 20,
	}
}

func (o HertzServerOptions) Validate() error {
	var bodyErr error
	if o.MaxRequestBodySize <= 0 {
		bodyErr = newValueError("http.max_request_body_size", "> 0", o.MaxRequestBodySize)
	}
	return join(
		validateListenAddress("http.address", o.Address),
		positiveDuration("http.read_timeout", o.ReadTimeout),
		positiveDuration("http.write_timeout", o.WriteTimeout),
		positiveDuration("http.idle_timeout", o.IdleTimeout),
		positiveDuration("http.shutdown_timeout", o.ShutdownTimeout),
		bodyErr,
		o.TLS.Validate(),
	)
}

package option

import (
	"fmt"
	"net"
	"strings"
	"time"
)

type PostgreSQLOptions struct {
	DSN             string        `mapstructure:"dsn" json:"dsn" yaml:"dsn"`
	Host            string        `mapstructure:"host" json:"host" yaml:"host"`
	Port            int           `mapstructure:"port" json:"port" yaml:"port"`
	User            string        `mapstructure:"user" json:"user" yaml:"user"`
	Password        string        `mapstructure:"password" json:"password" yaml:"password"`
	Database        string        `mapstructure:"database" json:"database" yaml:"database"`
	SSLMode         string        `mapstructure:"ssl_mode" json:"ssl_mode" yaml:"ssl_mode"`
	TimeZone        string        `mapstructure:"time_zone" json:"time_zone" yaml:"time_zone"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns" json:"max_idle_conns" yaml:"max_idle_conns"`
	MaxOpenConns    int           `mapstructure:"max_open_conns" json:"max_open_conns" yaml:"max_open_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime" json:"conn_max_lifetime" yaml:"conn_max_lifetime"`
	ConnMaxIdleTime time.Duration `mapstructure:"conn_max_idle_time" json:"conn_max_idle_time" yaml:"conn_max_idle_time"`
	ConnectTimeout  time.Duration `mapstructure:"connect_timeout" json:"connect_timeout" yaml:"connect_timeout"`
	SlowThreshold   time.Duration `mapstructure:"slow_threshold" json:"slow_threshold" yaml:"slow_threshold"`
	PrepareStmt     bool          `mapstructure:"prepare_stmt" json:"prepare_stmt" yaml:"prepare_stmt"`
	TLS             TLSOptions    `mapstructure:"tls" json:"tls" yaml:"tls"`
}

func NewPostgreSQLOptions() *PostgreSQLOptions {
	return &PostgreSQLOptions{
		Host:            "127.0.0.1",
		Port:            5432,
		User:            "knowledge_core",
		Database:        "knowledge_core",
		SSLMode:         "disable",
		TimeZone:        "UTC",
		MaxIdleConns:    10,
		MaxOpenConns:    100,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: 30 * time.Minute,
		ConnectTimeout:  5 * time.Second,
		SlowThreshold:   200 * time.Millisecond,
	}
}

func (o PostgreSQLOptions) Validate() error {
	var connectionErrors error
	if strings.TrimSpace(o.DSN) == "" {
		connectionErrors = join(
			validateEndpoint("postgres address", net.JoinHostPort(o.Host, fmt.Sprintf("%d", o.Port))),
			require("postgres.user", o.User),
			require("postgres.database", o.Database),
		)
	} else if o.TLS.Enabled {
		connectionErrors = fmt.Errorf("postgres.dsn and postgres.tls cannot be configured together; encode TLS settings in the DSN")
	}
	var sslModeErr error
	if !knownPostgreSQLSSLMode(o.SSLMode) {
		sslModeErr = fmt.Errorf("postgres.ssl_mode must be one of disable, allow, prefer, require, verify-ca, verify-full, got %q", o.SSLMode)
	}
	var poolErr error
	if o.MaxIdleConns < 0 {
		poolErr = join(poolErr, newValueError("postgres.max_idle_conns", ">= 0", o.MaxIdleConns))
	}
	if o.MaxOpenConns < 0 {
		poolErr = join(poolErr, newValueError("postgres.max_open_conns", ">= 0", o.MaxOpenConns))
	}
	if o.MaxOpenConns > 0 && o.MaxIdleConns > o.MaxOpenConns {
		poolErr = join(poolErr, fmt.Errorf("postgres.max_idle_conns must not exceed postgres.max_open_conns"))
	}
	var tlsModeErr error
	if o.TLS.Enabled && (o.SSLMode == "disable" || o.SSLMode == "allow" || o.SSLMode == "prefer") {
		tlsModeErr = fmt.Errorf("postgres.tls.enabled requires ssl_mode=require, verify-ca, or verify-full")
	}
	return join(
		connectionErrors,
		sslModeErr,
		poolErr,
		nonNegativeDuration("postgres.conn_max_lifetime", o.ConnMaxLifetime),
		nonNegativeDuration("postgres.conn_max_idle_time", o.ConnMaxIdleTime),
		positiveDuration("postgres.connect_timeout", o.ConnectTimeout),
		positiveDuration("postgres.slow_threshold", o.SlowThreshold),
		require("postgres.time_zone", o.TimeZone),
		tlsModeErr,
		o.TLS.Validate(),
	)
}

func knownPostgreSQLSSLMode(value string) bool {
	switch value {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		return true
	default:
		return false
	}
}

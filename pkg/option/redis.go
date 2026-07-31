package option

import (
	"fmt"
	"math"
	"time"
)

type RedisOptions struct {
	Address      string        `mapstructure:"address" json:"address" yaml:"address"`
	Username     string        `mapstructure:"username" json:"username" yaml:"username"`
	Password     string        `mapstructure:"password" json:"password" yaml:"password"`
	DB           int           `mapstructure:"db" json:"db" yaml:"db"`
	DialTimeout  time.Duration `mapstructure:"dial_timeout" json:"dial_timeout" yaml:"dial_timeout"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout" json:"read_timeout" yaml:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout" json:"write_timeout" yaml:"write_timeout"`
	PoolSize     int           `mapstructure:"pool_size" json:"pool_size" yaml:"pool_size"`
	MinIdleConns int           `mapstructure:"min_idle_conns" json:"min_idle_conns" yaml:"min_idle_conns"`
	MaxRetries   int           `mapstructure:"max_retries" json:"max_retries" yaml:"max_retries"`
	TLS          TLSOptions    `mapstructure:"tls" json:"tls" yaml:"tls"`
}

func NewRedisOptions() *RedisOptions {
	return &RedisOptions{
		Address:      "127.0.0.1:6379",
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     20,
		MinIdleConns: 2,
		MaxRetries:   3,
	}
}

func (o RedisOptions) Validate() error {
	var dbErr error
	if o.DB < 0 || int64(o.DB) > math.MaxInt32 {
		dbErr = fmt.Errorf("redis.db must be between 0 and %d, got %d", int64(math.MaxInt32), o.DB)
	}
	var poolErr error
	if o.PoolSize <= 0 {
		poolErr = join(poolErr, newValueError("redis.pool_size", "> 0", o.PoolSize))
	}
	if o.MinIdleConns < 0 {
		poolErr = join(poolErr, newValueError("redis.min_idle_conns", ">= 0", o.MinIdleConns))
	}
	if o.PoolSize > 0 && o.MinIdleConns > o.PoolSize {
		poolErr = join(poolErr, fmt.Errorf("redis.min_idle_conns must not exceed redis.pool_size"))
	}
	if o.MaxRetries < -1 {
		poolErr = join(poolErr, newValueError("redis.max_retries", ">= -1", o.MaxRetries))
	}
	return join(
		validateEndpoint("redis.address", o.Address),
		dbErr,
		positiveDuration("redis.dial_timeout", o.DialTimeout),
		positiveDuration("redis.read_timeout", o.ReadTimeout),
		positiveDuration("redis.write_timeout", o.WriteTimeout),
		poolErr,
		o.TLS.Validate(),
	)
}

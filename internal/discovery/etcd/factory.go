package etcd

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/kitex/pkg/discovery"
	"github.com/cloudwego/kitex/pkg/registry"
	registryetcd "github.com/kitex-contrib/registry-etcd"
)

type Config struct {
	Endpoints   []string
	Prefix      string
	Username    string
	Password    string
	DialTimeout time.Duration
}

func NewRegistry(cfg Config) (registry.Registry, error) {
	options, err := options(cfg)
	if err != nil {
		return nil, err
	}
	result, err := registryetcd.NewEtcdRegistry(cfg.Endpoints, options...)
	if err != nil {
		return nil, fmt.Errorf("create Kitex etcd registry: %w", err)
	}
	return result, nil
}

func NewResolver(cfg Config) (discovery.Resolver, error) {
	options, err := options(cfg)
	if err != nil {
		return nil, err
	}
	result, err := registryetcd.NewEtcdResolver(cfg.Endpoints, options...)
	if err != nil {
		return nil, fmt.Errorf("create Kitex etcd resolver: %w", err)
	}
	return result, nil
}

func options(cfg Config) ([]registryetcd.Option, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, errors.New("create Kitex etcd discovery: endpoints are required")
	}
	prefix := strings.TrimRight(cfg.Prefix, "/")
	if prefix == "" {
		return nil, errors.New("create Kitex etcd discovery: prefix is required")
	}
	result := []registryetcd.Option{registryetcd.WithEtcdServicePrefix(prefix)}
	if cfg.Username != "" || cfg.Password != "" {
		result = append(result, registryetcd.WithAuthOpt(cfg.Username, cfg.Password))
	}
	if cfg.DialTimeout > 0 {
		result = append(result, registryetcd.WithDialTimeoutOpt(cfg.DialTimeout))
	}
	return result, nil
}

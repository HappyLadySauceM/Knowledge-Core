package etcd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	"github.com/cloudwego/kitex/pkg/discovery"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type resolverClient interface {
	Get(context.Context, string, ...clientv3.OpOption) (*clientv3.GetResponse, error)
}

type ResolverResources struct {
	Resolver discovery.Resolver

	client    *clientv3.Client
	endpoints []string
	timeout   time.Duration
	closeOnce sync.Once
	closeErr  error
}

func OpenResolver(
	ctx context.Context,
	opts option.EtcdOptions,
	logger *slog.Logger,
) (*ResolverResources, error) {
	if ctx == nil {
		return nil, errors.New("open etcd resolver: context is required")
	}
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("open etcd resolver: invalid options: %w", err)
	}
	client, err := newEtcdClient(opts)
	if err != nil {
		return nil, err
	}
	resolver := &etcdResolver{
		client:  client,
		prefix:  strings.TrimRight(opts.Prefix, "/"),
		timeout: opts.RequestTimeout,
	}
	resources := &ResolverResources{
		Resolver:  resolver,
		client:    client,
		endpoints: append([]string(nil), opts.Endpoints...),
		timeout:   opts.RequestTimeout,
	}
	if err := resources.Ping(ctx); err != nil {
		return nil, errors.Join(err, resources.Close())
	}
	if logger != nil {
		logger.DebugContext(ctx, "etcd resolver connected",
			slog.Int("endpoints", len(opts.Endpoints)),
			slog.String("prefix", opts.Prefix),
		)
	}
	return resources, nil
}

func (r *ResolverResources) Ping(ctx context.Context) error {
	if r == nil || r.client == nil {
		return errors.New("ping etcd resolver: client is nil")
	}
	if ctx == nil {
		return errors.New("ping etcd resolver: context is required")
	}
	var joined error
	for _, endpoint := range r.endpoints {
		checkCtx, cancel := context.WithTimeout(ctx, r.timeout)
		_, err := r.client.Status(checkCtx, endpoint)
		cancel()
		if err == nil {
			return nil
		}
		joined = errors.Join(joined, fmt.Errorf("endpoint %q: %w", endpoint, err))
		if ctx.Err() != nil {
			break
		}
	}
	return fmt.Errorf("ping etcd resolver: %w", joined)
}

func (r *ResolverResources) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	r.closeOnce.Do(func() { r.closeErr = closeEtcdClient(r.client) })
	return r.closeErr
}

type etcdResolver struct {
	client  resolverClient
	prefix  string
	timeout time.Duration
}

func (r *etcdResolver) Target(_ context.Context, target rpcinfo.EndpointInfo) string {
	if target == nil {
		return ""
	}
	return target.ServiceName()
}

func (r *etcdResolver) Resolve(ctx context.Context, serviceName string) (discovery.Result, error) {
	serviceName = strings.TrimSpace(serviceName)
	if r == nil || r.client == nil || serviceName == "" || strings.Contains(serviceName, "/") {
		return discovery.Result{}, errors.New("resolve etcd service: resolver and service name are required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	resolveCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	key := r.prefix + "/" + serviceName + "/"
	response, err := r.client.Get(resolveCtx, key, clientv3.WithPrefix())
	if err != nil {
		return discovery.Result{}, fmt.Errorf("resolve etcd service %q: %w", serviceName, err)
	}
	instances := make([]discovery.Instance, 0, len(response.Kvs))
	for _, item := range response.Kvs {
		var record instanceInfo
		if err := jsoncodec.Unmarshal(item.Value, &record); err != nil {
			return discovery.Result{}, fmt.Errorf("resolve etcd service %q: decode instance: %w", serviceName, err)
		}
		if record.Network == "" || record.Address == "" || record.Weight <= 0 {
			return discovery.Result{}, fmt.Errorf("resolve etcd service %q: invalid instance record", serviceName)
		}
		if _, _, err := net.SplitHostPort(record.Address); err != nil {
			return discovery.Result{}, fmt.Errorf("resolve etcd service %q: invalid instance address: %w", serviceName, err)
		}
		instances = append(instances, discovery.NewInstance(record.Network, record.Address, record.Weight, cloneStringMap(record.Tags)))
	}
	if len(instances) == 0 {
		return discovery.Result{}, fmt.Errorf("resolve etcd service %q: no instances are registered", serviceName)
	}
	return discovery.Result{Cacheable: true, CacheKey: serviceName, Instances: instances}, nil
}

func (r *etcdResolver) Diff(cacheKey string, previous, next discovery.Result) (discovery.Change, bool) {
	return discovery.DefaultDiff(cacheKey, previous, next)
}

func (r *etcdResolver) Name() string { return "knowledge-core-etcd" }

func cloneStringMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

var _ discovery.Resolver = (*etcdResolver)(nil)

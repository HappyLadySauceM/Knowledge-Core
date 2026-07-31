package etcd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	kitexregistry "github.com/cloudwego/kitex/pkg/registry"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	leaseTTLEnvironment = "KITEX_ETCD_REGISTRY_LEASE_TTL"
	ipEnvironment       = "KITEX_IP_TO_REGISTRY"
	portEnvironment     = "KITEX_PORT_TO_REGISTRY"
	defaultLeaseTTL     = int64(60)
)

var (
	errEtcdRegistryClosed = errors.New("etcd registry is closed")
	// ErrRegistrationKeepAliveStopped reports that etcd can still be reached,
	// but the registered service lease is no longer being renewed.
	ErrRegistrationKeepAliveStopped = errors.New("etcd registration keepalive stopped")
)

// registryClient is the subset of clientv3.Client used for registration. The
// production implementation is the same client explicitly owned by Resources.
type registryClient interface {
	Put(context.Context, string, string, ...clientv3.OpOption) (*clientv3.PutResponse, error)
	Delete(context.Context, string, ...clientv3.OpOption) (*clientv3.DeleteResponse, error)
	Grant(context.Context, int64) (*clientv3.LeaseGrantResponse, error)
	KeepAlive(context.Context, clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error)
	Revoke(context.Context, clientv3.LeaseID) (*clientv3.LeaseRevokeResponse, error)
}

// etcdRegistry implements Kitex's fixed etcd registration contract without
// hiding a second client inside a third-party registry implementation.
type etcdRegistry struct {
	client         registryClient
	prefix         string
	requestTimeout time.Duration
	leaseTTL       int64

	mu           sync.Mutex
	closed       bool
	registration *etcdRegistration

	healthMu  sync.RWMutex
	healthErr error
}

type etcdRegistration struct {
	key           string
	leaseID       clientv3.LeaseID
	keepAliveStop context.CancelFunc
	keepAliveDone <-chan struct{}
}

type instanceInfo struct {
	Network string            `json:"network"`
	Address string            `json:"address"`
	Weight  int               `json:"weight"`
	Tags    map[string]string `json:"tags"`
}

var _ kitexregistry.Registry = (*etcdRegistry)(nil)

func newEtcdRegistry(client registryClient, opts option.EtcdOptions) (*etcdRegistry, error) {
	if client == nil {
		return nil, errors.New("create Kitex etcd registry: client is nil")
	}
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("create Kitex etcd registry: invalid options: %w", err)
	}
	return &etcdRegistry{
		client:         client,
		prefix:         strings.TrimRight(opts.Prefix, "/"),
		requestTimeout: opts.RequestTimeout,
		leaseTTL:       registryLeaseTTL(),
	}, nil
}

func (r *etcdRegistry) Register(info *kitexregistry.Info) error {
	key, value, err := r.registrationRecord(info)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errEtcdRegistryClosed
	}
	if r.registration != nil {
		return errors.New("register etcd service: a registration is already active")
	}

	leaseID, err := r.grantLease()
	if err != nil {
		return err
	}
	if err := r.put(key, value, leaseID); err != nil {
		return errors.Join(err, r.revokeAfterFailedRegistration(leaseID))
	}

	keepAliveCtx, keepAliveStop := context.WithCancel(context.Background())
	responses, err := r.client.KeepAlive(keepAliveCtx, leaseID)
	if err != nil {
		keepAliveStop()
		return errors.Join(
			fmt.Errorf("register etcd service: keep lease alive: %w", err),
			r.deleteAfterFailedRegistration(key),
			r.revokeAfterFailedRegistration(leaseID),
		)
	}
	keepAliveDone := make(chan struct{})
	r.setHealthError(nil)
	go monitorKeepAlive(keepAliveCtx, leaseID, responses, keepAliveDone, r.setHealthError)
	r.registration = &etcdRegistration{
		key:           key,
		leaseID:       leaseID,
		keepAliveStop: keepAliveStop,
		keepAliveDone: keepAliveDone,
	}
	return nil
}

func (r *etcdRegistry) Deregister(info *kitexregistry.Info) error {
	if info == nil || info.ServiceName == "" {
		return errors.New("missing service name in Deregister")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errEtcdRegistryClosed
	}
	registration := r.registration
	if registration == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.requestTimeout)
	_, err := r.client.Delete(ctx, registration.key)
	cancel()
	if err != nil {
		return fmt.Errorf("deregister etcd service: delete key: %w", err)
	}

	registration.keepAliveStop()
	<-registration.keepAliveDone
	ctx, cancel = context.WithTimeout(context.Background(), r.requestTimeout)
	_, err = r.client.Revoke(ctx, registration.leaseID)
	cancel()
	if err != nil {
		return fmt.Errorf("deregister etcd service: revoke lease: %w", err)
	}
	r.registration = nil
	r.setHealthError(nil)
	return nil
}

func (r *etcdRegistry) close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	registration := r.registration
	r.registration = nil
	if registration != nil {
		registration.keepAliveStop()
	}
	r.mu.Unlock()
	if registration != nil {
		<-registration.keepAliveDone
	}
}

func (r *etcdRegistry) registrationRecord(info *kitexregistry.Info) (string, string, error) {
	if err := validateRegistryInfo(info); err != nil {
		return "", "", err
	}
	address, err := registrationAddress(info.Addr)
	if err != nil {
		return "", "", err
	}
	value, err := jsoncodec.Marshal(instanceInfo{
		Network: info.Addr.Network(),
		Address: address,
		Weight:  info.Weight,
		Tags:    info.Tags,
	})
	if err != nil {
		return "", "", fmt.Errorf("register etcd service: marshal instance: %w", err)
	}
	return serviceKey(r.prefix, info.ServiceName, address), string(value), nil
}

func (r *etcdRegistry) grantLease() (clientv3.LeaseID, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.requestTimeout)
	defer cancel()
	response, err := r.client.Grant(ctx, r.leaseTTL)
	if err != nil {
		return clientv3.NoLease, fmt.Errorf("register etcd service: grant lease: %w", err)
	}
	return response.ID, nil
}

func (r *etcdRegistry) put(key, value string, leaseID clientv3.LeaseID) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.requestTimeout)
	defer cancel()
	if _, err := r.client.Put(ctx, key, value, clientv3.WithLease(leaseID)); err != nil {
		return fmt.Errorf("register etcd service: put key: %w", err)
	}
	return nil
}

func (r *etcdRegistry) deleteAfterFailedRegistration(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.requestTimeout)
	defer cancel()
	if _, err := r.client.Delete(ctx, key); err != nil {
		return fmt.Errorf("clean up failed etcd registration: delete key: %w", err)
	}
	return nil
}

func (r *etcdRegistry) revokeAfterFailedRegistration(leaseID clientv3.LeaseID) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.requestTimeout)
	defer cancel()
	if _, err := r.client.Revoke(ctx, leaseID); err != nil {
		return fmt.Errorf("clean up failed etcd registration: revoke lease: %w", err)
	}
	return nil
}

func monitorKeepAlive(
	ctx context.Context,
	leaseID clientv3.LeaseID,
	responses <-chan *clientv3.LeaseKeepAliveResponse,
	done chan<- struct{},
	report func(error),
) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case response, ok := <-responses:
			if !ok {
				if ctx.Err() == nil {
					report(fmt.Errorf("%w: lease %x response channel closed", ErrRegistrationKeepAliveStopped, leaseID))
				}
				return
			}
			if response == nil || response.ID != leaseID || response.TTL <= 0 {
				report(fmt.Errorf("%w: lease %x received an invalid response", ErrRegistrationKeepAliveStopped, leaseID))
				return
			}
		}
	}
}

func (r *etcdRegistry) setHealthError(err error) {
	r.healthMu.Lock()
	r.healthErr = err
	r.healthMu.Unlock()
}

func (r *etcdRegistry) healthError() error {
	if r == nil {
		return nil
	}
	r.healthMu.RLock()
	defer r.healthMu.RUnlock()
	return r.healthErr
}

func validateRegistryInfo(info *kitexregistry.Info) error {
	if info == nil || info.ServiceName == "" {
		return errors.New("missing service name in Register")
	}
	if strings.Contains(info.ServiceName, "/") {
		return errors.New("service name registered with etcd should not include character '/'")
	}
	if info.Addr == nil {
		return errors.New("missing addr in Register")
	}
	return nil
}

func registrationAddress(address net.Addr) (string, error) {
	host, port, err := net.SplitHostPort(address.String())
	if err != nil {
		return "", fmt.Errorf("parse registry info address: %w", err)
	}
	if host == "" || host == "::" {
		host, err = localIPv4Host()
		if err != nil {
			return "", fmt.Errorf("parse registry info address: %w", err)
		}
	}
	if configuredHost, ok := os.LookupEnv(ipEnvironment); ok && configuredHost != "" {
		host = configuredHost
	}
	if configuredPort, ok := os.LookupEnv(portEnvironment); ok && configuredPort != "" {
		port = configuredPort
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil {
		return "", fmt.Errorf("parse registry info port: %w", err)
	}
	return fmt.Sprintf("%s:%d", host, parsedPort), nil
}

func localIPv4Host() (string, error) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, address := range addresses {
		ipNet, ok := address.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if ipv4 := ipNet.IP.To4(); ipv4 != nil {
			return ipv4.String(), nil
		}
	}
	return "", errors.New("no non-loopback IPv4 address found")
}

func serviceKey(prefix, serviceName, address string) string {
	return prefix + "/" + serviceName + "/" + address
}

func registryLeaseTTL() int64 {
	ttl := defaultLeaseTTL
	if value, ok := os.LookupEnv(leaseTTLEnvironment); ok {
		if parsed, err := strconv.Atoi(value); err == nil {
			ttl = int64(parsed)
		}
	}
	return ttl
}

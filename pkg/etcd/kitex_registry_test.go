package etcd

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	kitexregistry "github.com/cloudwego/kitex/pkg/registry"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestEtcdRegistryUsesRegistryEtcdCompatibleRecordAndOwnedLease(t *testing.T) {
	t.Setenv(leaseTTLEnvironment, "75")
	t.Setenv(ipEnvironment, "")
	t.Setenv(portEnvironment, "")

	client := newFakeRegistryClient(41)
	opts := *option.NewEtcdOptions()
	opts.Prefix += "/"
	registry, err := newEtcdRegistry(client, opts)
	if err != nil {
		t.Fatalf("newEtcdRegistry() error = %v", err)
	}
	info := &kitexregistry.Info{
		ServiceName: "identity",
		Addr:        &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9001},
		Weight:      17,
		Tags:        map[string]string{"environment": "test"},
	}

	if err := registry.Register(info); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	state := client.snapshot()
	if got, want := state.grantTTLs, []int64{75}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Grant() TTLs = %v, want %v", got, want)
	}
	if got, want := state.puts, []fakePut{{
		key:         "/knowledge-core/development/registry/identity/127.0.0.1:9001",
		value:       `{"network":"tcp","address":"127.0.0.1:9001","weight":17,"tags":{"environment":"test"}}`,
		optionCount: 1,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Put() calls = %#v, want %#v", got, want)
	}
	if got, want := state.keepAliveLeases, []clientv3.LeaseID{41}; !reflect.DeepEqual(got, want) {
		t.Fatalf("KeepAlive() leases = %v, want %v", got, want)
	}

	if err := registry.Deregister(info); err != nil {
		t.Fatalf("Deregister() error = %v", err)
	}
	state = client.snapshot()
	if got, want := state.calls, []string{"grant", "put", "keepalive", "delete", "revoke"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("client calls = %v, want %v", got, want)
	}
	if got, want := state.deletes, []string{"/knowledge-core/development/registry/identity/127.0.0.1:9001"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Delete() keys = %v, want %v", got, want)
	}
	if got, want := state.revokedLeases, []clientv3.LeaseID{41}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Revoke() leases = %v, want %v", got, want)
	}
	if state.keepAliveContext == nil || !errors.Is(state.keepAliveContext.Err(), context.Canceled) {
		t.Fatalf("keepalive context error = %v, want cancellation", contextError(state.keepAliveContext))
	}
}

func TestEtcdRegistryUsesRegistryEtcdAddressOverrides(t *testing.T) {
	t.Setenv(ipEnvironment, "10.0.0.8")
	t.Setenv(portEnvironment, "7777")

	client := newFakeRegistryClient(42)
	registry := mustEtcdRegistry(t, client)
	info := &kitexregistry.Info{
		ServiceName: "identity",
		Addr:        &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9002},
	}
	if err := registry.Register(info); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	state := client.snapshot()
	if got, want := state.puts[0].key, "/knowledge-core/development/registry/identity/10.0.0.8:7777"; got != want {
		t.Fatalf("Put() key = %q, want %q", got, want)
	}
	if !strings.Contains(state.puts[0].value, `"address":"10.0.0.8:7777"`) {
		t.Fatalf("Put() value = %q, want overridden address", state.puts[0].value)
	}
	if err := registry.Deregister(info); err != nil {
		t.Fatalf("Deregister() error = %v", err)
	}
}

func TestRegistrationAddressValidatesOverrides(t *testing.T) {
	tests := []struct {
		name        string
		host        string
		port        string
		wantAddress string
		wantError   string
	}{
		{name: "IPv4", host: "10.0.0.8", port: "1", wantAddress: "10.0.0.8:1"},
		{name: "IPv6", host: "2001:db8::8", port: "65535", wantAddress: "[2001:db8::8]:65535"},
		{name: "invalid host", host: "identity.internal", port: "7777", wantError: "invalid IP address"},
		{name: "unspecified host", host: "0.0.0.0", port: "7777", wantError: "unspecified IP address"},
		{name: "non-numeric port", host: "10.0.0.8", port: "http", wantError: "parse registry info port"},
		{name: "zero port", host: "10.0.0.8", port: "0", wantError: "must be between 1 and 65535"},
		{name: "port above maximum", host: "10.0.0.8", port: "65536", wantError: "must be between 1 and 65535"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(ipEnvironment, test.host)
			t.Setenv(portEnvironment, test.port)

			got, err := registrationAddress(&net.TCPAddr{IP: net.IPv4zero, Port: 9002})
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("registrationAddress() error = %v, want containing %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("registrationAddress() error = %v", err)
			}
			if got != test.wantAddress {
				t.Fatalf("registrationAddress() = %q, want %q", got, test.wantAddress)
			}
		})
	}
}

func TestEtcdRegistryDeleteFailureKeepsRegistrationAliveForRetry(t *testing.T) {
	deleteErr := errors.New("delete failed")
	client := newFakeRegistryClient(43)
	client.deleteErrors = []error{deleteErr, nil}
	registry := mustEtcdRegistry(t, client)
	info := testRegistryInfo(9003)
	if err := registry.Register(info); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if err := registry.Deregister(info); !errors.Is(err, deleteErr) {
		t.Fatalf("first Deregister() error = %v, want %v", err, deleteErr)
	}
	state := client.snapshot()
	if err := contextError(state.keepAliveContext); err != nil {
		t.Fatalf("keepalive context error after failed delete = %v, want active", err)
	}
	if len(state.revokedLeases) != 0 {
		t.Fatalf("Revoke() leases after failed delete = %v, want none", state.revokedLeases)
	}

	if err := registry.Deregister(info); err != nil {
		t.Fatalf("second Deregister() error = %v", err)
	}
	state = client.snapshot()
	if got := len(state.deletes); got != 2 {
		t.Fatalf("Delete() calls = %d, want 2", got)
	}
	if got, want := state.revokedLeases, []clientv3.LeaseID{43}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Revoke() leases = %v, want %v", got, want)
	}
}

func TestEtcdRegistryRevokeFailureCanBeRetried(t *testing.T) {
	revokeErr := errors.New("revoke failed")
	client := newFakeRegistryClient(44)
	client.revokeErrors = []error{revokeErr, nil}
	registry := mustEtcdRegistry(t, client)
	info := testRegistryInfo(9004)
	if err := registry.Register(info); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if err := registry.Deregister(info); !errors.Is(err, revokeErr) {
		t.Fatalf("first Deregister() error = %v, want %v", err, revokeErr)
	}
	if err := registry.Deregister(info); err != nil {
		t.Fatalf("second Deregister() error = %v", err)
	}
	state := client.snapshot()
	if got, want := state.revokedLeases, []clientv3.LeaseID{44, 44}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Revoke() leases = %v, want %v", got, want)
	}
}

func TestEtcdRegistryKeepAliveFailureCleansRegistration(t *testing.T) {
	keepAliveErr := errors.New("keepalive failed")
	client := newFakeRegistryClient(45)
	client.keepAliveErr = keepAliveErr
	registry := mustEtcdRegistry(t, client)

	if err := registry.Register(testRegistryInfo(9005)); !errors.Is(err, keepAliveErr) {
		t.Fatalf("Register() error = %v, want %v", err, keepAliveErr)
	}
	state := client.snapshot()
	if got, want := state.calls, []string{"grant", "put", "keepalive", "delete", "revoke"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("client calls = %v, want cleanup sequence %v", got, want)
	}
	if registry.registration != nil {
		t.Fatal("failed Register() retained registration state")
	}
}

func TestEtcdRegistryCloseStopsKeepAlive(t *testing.T) {
	client := newFakeRegistryClient(46)
	registry := mustEtcdRegistry(t, client)
	if err := registry.Register(testRegistryInfo(9006)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	registry.close()
	state := client.snapshot()
	if state.keepAliveContext == nil || !errors.Is(state.keepAliveContext.Err(), context.Canceled) {
		t.Fatalf("keepalive context error = %v, want cancellation", contextError(state.keepAliveContext))
	}
	if err := registry.Register(testRegistryInfo(9006)); !errors.Is(err, errEtcdRegistryClosed) {
		t.Fatalf("Register() after close error = %v, want closed", err)
	}
}

func TestResourcesPingFailsWhenRegistrationKeepAliveStops(t *testing.T) {
	client := newFakeRegistryClient(47)
	registry := mustEtcdRegistry(t, client)
	if err := registry.Register(testRegistryInfo(9007)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	registration := registry.registration
	close(client.keepAlive)
	<-registration.keepAliveDone
	t.Cleanup(registry.close)

	resources := &Resources{registry: registry}
	if err := resources.Ping(context.Background()); !errors.Is(err, ErrRegistrationKeepAliveStopped) {
		t.Fatalf("Ping() error = %v, want keepalive stopped", err)
	}
}

type fakeRegistryClient struct {
	mu sync.Mutex

	leaseID         clientv3.LeaseID
	keepAlive       chan *clientv3.LeaseKeepAliveResponse
	keepAliveErr    error
	deleteErrors    []error
	revokeErrors    []error
	deleteAttempts  int
	revokeAttempts  int
	calls           []string
	grantTTLs       []int64
	puts            []fakePut
	deletes         []string
	keepAliveLeases []clientv3.LeaseID
	revokedLeases   []clientv3.LeaseID
	keepAliveCtx    context.Context
}

type fakePut struct {
	key         string
	value       string
	optionCount int
}

type fakeRegistryState struct {
	calls            []string
	grantTTLs        []int64
	puts             []fakePut
	deletes          []string
	keepAliveLeases  []clientv3.LeaseID
	revokedLeases    []clientv3.LeaseID
	keepAliveContext context.Context
}

func newFakeRegistryClient(leaseID clientv3.LeaseID) *fakeRegistryClient {
	return &fakeRegistryClient{
		leaseID:   leaseID,
		keepAlive: make(chan *clientv3.LeaseKeepAliveResponse),
	}
}

func (c *fakeRegistryClient) Put(_ context.Context, key, value string, opts ...clientv3.OpOption) (*clientv3.PutResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, "put")
	c.puts = append(c.puts, fakePut{key: key, value: value, optionCount: len(opts)})
	return &clientv3.PutResponse{}, nil
}

func (c *fakeRegistryClient) Delete(_ context.Context, key string, _ ...clientv3.OpOption) (*clientv3.DeleteResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, "delete")
	c.deletes = append(c.deletes, key)
	err := errorAt(c.deleteErrors, c.deleteAttempts)
	c.deleteAttempts++
	return &clientv3.DeleteResponse{}, err
}

func (c *fakeRegistryClient) Grant(_ context.Context, ttl int64) (*clientv3.LeaseGrantResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, "grant")
	c.grantTTLs = append(c.grantTTLs, ttl)
	return &clientv3.LeaseGrantResponse{ID: c.leaseID, TTL: ttl}, nil
}

func (c *fakeRegistryClient) KeepAlive(ctx context.Context, leaseID clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, "keepalive")
	c.keepAliveLeases = append(c.keepAliveLeases, leaseID)
	c.keepAliveCtx = ctx
	return c.keepAlive, c.keepAliveErr
}

func (c *fakeRegistryClient) Revoke(_ context.Context, leaseID clientv3.LeaseID) (*clientv3.LeaseRevokeResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, "revoke")
	c.revokedLeases = append(c.revokedLeases, leaseID)
	err := errorAt(c.revokeErrors, c.revokeAttempts)
	c.revokeAttempts++
	return &clientv3.LeaseRevokeResponse{}, err
}

func (c *fakeRegistryClient) snapshot() fakeRegistryState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fakeRegistryState{
		calls:            append([]string(nil), c.calls...),
		grantTTLs:        append([]int64(nil), c.grantTTLs...),
		puts:             append([]fakePut(nil), c.puts...),
		deletes:          append([]string(nil), c.deletes...),
		keepAliveLeases:  append([]clientv3.LeaseID(nil), c.keepAliveLeases...),
		revokedLeases:    append([]clientv3.LeaseID(nil), c.revokedLeases...),
		keepAliveContext: c.keepAliveCtx,
	}
}

func mustEtcdRegistry(t *testing.T, client registryClient) *etcdRegistry {
	t.Helper()
	registry, err := newEtcdRegistry(client, *option.NewEtcdOptions())
	if err != nil {
		t.Fatalf("newEtcdRegistry() error = %v", err)
	}
	return registry
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func errorAt(errs []error, index int) error {
	if index >= len(errs) {
		return nil
	}
	return errs[index]
}

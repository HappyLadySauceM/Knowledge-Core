package etcd

import (
	"context"
	"testing"
	"time"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type resolverClientStub struct {
	response *clientv3.GetResponse
	err      error
}

func (s resolverClientStub) Get(context.Context, string, ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	return s.response, s.err
}

func TestEtcdResolverDecodesRegisteredInstance(t *testing.T) {
	encoded, err := jsoncodec.Marshal(instanceInfo{
		Network: "tcp", Address: "127.0.0.1:8881", Weight: 10,
		Tags: map[string]string{"environment": "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver := &etcdResolver{
		client: resolverClientStub{response: &clientv3.GetResponse{Kvs: []*mvccpb.KeyValue{{Value: encoded}}}},
		prefix: "/registry", timeout: time.Second,
	}
	result, err := resolver.Resolve(context.Background(), "knowledge-core.identity")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !result.Cacheable || result.CacheKey != "knowledge-core.identity" || len(result.Instances) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if address := result.Instances[0].Address(); address.String() != "127.0.0.1:8881" {
		t.Fatalf("address = %v", address)
	}
}

func TestEtcdResolverRejectsEmptyResult(t *testing.T) {
	resolver := &etcdResolver{
		client: resolverClientStub{response: &clientv3.GetResponse{}}, prefix: "/registry", timeout: time.Second,
	}
	if _, err := resolver.Resolve(context.Background(), "knowledge-core.identity"); err == nil {
		t.Fatal("Resolve() accepted an empty result")
	}
}

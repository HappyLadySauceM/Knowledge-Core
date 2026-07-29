package etcd_test

import (
	"context"
	"testing"

	etcdconfig "github.com/HappyLadySauce/Knowledge-Core/internal/config/etcd"
)

func TestOpenValidatesBootstrapConfig(t *testing.T) {
	if _, err := etcdconfig.Open(context.Background(), etcdconfig.Config{}); err == nil {
		t.Fatal("Open() accepted empty endpoints")
	}
	if _, err := etcdconfig.Open(context.Background(), etcdconfig.Config{Endpoints: []string{"localhost:2379"}}); err == nil {
		t.Fatal("Open() accepted an empty prefix")
	}
}

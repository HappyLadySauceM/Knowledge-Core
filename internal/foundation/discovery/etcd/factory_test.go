package etcd_test

import (
	"testing"

	discoveryetcd "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/discovery/etcd"
)

func TestFactoryValidatesBootstrapConfig(t *testing.T) {
	if _, err := discoveryetcd.NewRegistry(discoveryetcd.Config{}); err == nil {
		t.Fatal("NewRegistry() accepted empty endpoints")
	}
	if _, err := discoveryetcd.NewResolver(discoveryetcd.Config{Endpoints: []string{"localhost:2379"}}); err == nil {
		t.Fatal("NewResolver() accepted an empty prefix")
	}
}

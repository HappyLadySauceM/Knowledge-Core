package client

import "testing"

func TestNewIdentityRequiresResolver(t *testing.T) {
	if _, err := NewIdentity(nil, nil); err == nil {
		t.Fatal("NewIdentity() accepted a nil resolver")
	}
}

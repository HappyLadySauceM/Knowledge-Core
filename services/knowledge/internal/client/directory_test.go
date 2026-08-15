package client

import (
	"context"
	"errors"
	"testing"

	identityv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity/identityservice"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/circuit"
	"github.com/cloudwego/kitex/client/callopt"
)

type identityRPCStub struct {
	identityservice.Client
	err error
}

func (s *identityRPCStub) GetCurrentUser(
	context.Context,
	*identityv1.CurrentUserRequest,
	...callopt.Option,
) (*identityv1.User, error) {
	return nil, s.err
}

func TestDirectoryMapsCircuitOpenToUnavailable(t *testing.T) {
	directory, err := NewDirectory(&identityRPCStub{err: circuit.ErrOpen})
	if err != nil {
		t.Fatalf("NewDirectory() error = %v", err)
	}
	_, err = directory.CurrentUser(context.Background())
	if !errors.Is(err, ErrDirectoryUnavailable) {
		t.Fatalf("CurrentUser() error = %v, want %v", err, ErrDirectoryUnavailable)
	}
	if !errors.Is(err, circuit.ErrOpen) {
		t.Fatalf("CurrentUser() error = %v, want wrapped %v", err, circuit.ErrOpen)
	}
}

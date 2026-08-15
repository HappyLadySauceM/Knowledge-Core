package client

import (
	"context"
	"errors"
	"testing"

	collaborationv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/collaboration"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/collaboration/collaborationservice"
	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/circuit"
	"github.com/cloudwego/kitex/client/callopt"
)

type collaborationRPCStub struct {
	collaborationservice.Client
	purgeRequest *collaborationv1.PurgeDocumentRequest
	err          error
}

func (s *collaborationRPCStub) Ping(context.Context, *commonv1.PingRequest, ...callopt.Option) (*commonv1.PingResponse, error) {
	return &commonv1.PingResponse{Service: "collaboration", Status: "ready"}, nil
}

func (s *collaborationRPCStub) PurgeDocument(
	_ context.Context,
	request *collaborationv1.PurgeDocumentRequest,
	_ ...callopt.Option,
) error {
	s.purgeRequest = request
	return s.err
}

func TestCollaborationUsesTypedPingAndPurgeRPC(t *testing.T) {
	rpc := &collaborationRPCStub{}
	client := &Collaboration{client: rpc}
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	documentID := "0198a3c0-0000-7000-8000-000000000001"
	if err := client.PurgeDocument(context.Background(), documentID); err != nil {
		t.Fatalf("PurgeDocument() error = %v", err)
	}
	if rpc.purgeRequest == nil || rpc.purgeRequest.DocumentId != documentID {
		t.Fatalf("purge request = %#v", rpc.purgeRequest)
	}
	if err := client.PurgeDocument(context.Background(), "not-a-uuid"); err == nil {
		t.Fatal("PurgeDocument() accepted an invalid document ID")
	}
}

func TestCollaborationPurgePreservesCircuitOpen(t *testing.T) {
	rpc := &collaborationRPCStub{err: circuit.ErrOpen}
	client := &Collaboration{client: rpc}
	err := client.PurgeDocument(context.Background(), "0198a3c0-0000-7000-8000-000000000001")
	if !errors.Is(err, circuit.ErrOpen) {
		t.Fatalf("PurgeDocument() error = %v, want wrapped %v", err, circuit.ErrOpen)
	}
}

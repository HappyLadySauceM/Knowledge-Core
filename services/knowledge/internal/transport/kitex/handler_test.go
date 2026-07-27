package kitex_test

import (
	"context"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	knowledgekitex "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/transport/kitex"
)

func TestPing(t *testing.T) {
	response, err := knowledgekitex.NewHandler().Ping(context.Background(), &common.PingRequest{})
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if response.Service != "knowledge" || response.Status != "ok" || response.UnixTime == 0 {
		t.Fatalf("Ping() = %#v", response)
	}
}

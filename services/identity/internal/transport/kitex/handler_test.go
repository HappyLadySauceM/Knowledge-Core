package kitex_test

import (
	"context"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	identitykitex "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/transport/kitex"
)

func TestPing(t *testing.T) {
	response, err := identitykitex.NewHandler().Ping(context.Background(), &common.PingRequest{})
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if response.Service != "identity" || response.Status != "ok" || response.UnixTime == 0 {
		t.Fatalf("Ping() = %#v", response)
	}
}

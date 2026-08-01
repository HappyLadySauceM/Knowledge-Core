package metrics_test

import (
	"context"
	"errors"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/cloudwego/kitex/pkg/endpoint"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
)

func TestKitexMiddlewareClassifiesOutcomes(t *testing.T) {
	registry := newRegistry(t, "rpc")
	tests := []struct {
		name         string
		err          error
		outcome      string
		businessCode string
	}{
		{name: "success", outcome: "ok", businessCode: "0"},
		{name: "business", err: kerrors.NewBizStatusError(20002, "conflict"), outcome: "business_error", businessCode: "20002"},
		{name: "transport", err: errors.New("connection failed"), outcome: "error", businessCode: "transport"},
	}
	for _, test := range tests {
		invocation := rpcinfo.NewInvocation("identity.IdentityService", "Register")
		ctx := rpcinfo.NewCtxWithRPCInfo(
			context.Background(),
			rpcinfo.NewRPCInfo(nil, nil, invocation, nil, nil),
		)
		next := endpoint.Endpoint(func(context.Context, any, any) error { return test.err })
		if err := metrics.KitexServerMiddleware(registry)(next)(ctx, nil, nil); !errors.Is(err, test.err) {
			t.Fatalf("%s middleware error = %v, want %v", test.name, err, test.err)
		}
		if err := metrics.KitexClientMiddleware(registry)(next)(ctx, nil, nil); !errors.Is(err, test.err) {
			t.Fatalf("%s client middleware error = %v, want %v", test.name, err, test.err)
		}
	}

	family := gatherFamily(t, registry, "knowledge_core_rpc_server_requests_total")
	for _, test := range tests {
		if got := counterValue(family, map[string]string{
			"rpc_service":   "identity.IdentityService",
			"rpc_method":    "Register",
			"outcome":       test.outcome,
			"business_code": test.businessCode,
		}); got != 1 {
			t.Fatalf("%s RPC request counter = %v, want 1", test.name, got)
		}
	}

	clientFamily := gatherFamily(t, registry, "knowledge_core_rpc_client_requests_total")
	for _, test := range tests {
		if got := counterValue(clientFamily, map[string]string{
			"rpc_service":   "identity.IdentityService",
			"rpc_method":    "Register",
			"outcome":       test.outcome,
			"business_code": test.businessCode,
		}); got != 1 {
			t.Fatalf("%s RPC client request counter = %v, want 1", test.name, got)
		}
	}
}

package metrics

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/circuit"
	"github.com/cloudwego/kitex/pkg/endpoint"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
)

func KitexServerMiddleware(registry *Registry) endpoint.Middleware {
	return kitexMiddleware(registry, false)
}

func KitexClientMiddleware(registry *Registry) endpoint.Middleware {
	return kitexMiddleware(registry, true)
}

func kitexMiddleware(registry *Registry, client bool) endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, request, response any) error {
			if registry == nil {
				return next(ctx, request, response)
			}

			service, method := kitexRPCDetails(ctx)
			inFlight := registry.rpcInFlight.WithLabelValues(service, method)
			if client {
				inFlight = registry.rpcClientInFlight.WithLabelValues(service, method)
			}
			inFlight.Inc()
			started := time.Now()
			defer inFlight.Dec()

			err := next(ctx, request, response)
			outcome, businessCode := kitexRPCOutcome(ctx, err)
			if client {
				registry.rpcClientRequests.WithLabelValues(service, method, outcome, businessCode).Inc()
				registry.rpcClientDuration.WithLabelValues(service, method, outcome).Observe(time.Since(started).Seconds())
			} else {
				registry.rpcRequests.WithLabelValues(service, method, outcome, businessCode).Inc()
				registry.rpcDuration.WithLabelValues(service, method, outcome).Observe(time.Since(started).Seconds())
			}
			return err
		}
	}
}

func kitexRPCOutcome(ctx context.Context, err error) (string, string) {
	if businessError, ok := kerrors.FromBizStatusError(err); ok {
		return "business_error", strconv.FormatInt(int64(businessError.BizStatusCode()), 10)
	}
	if info := rpcinfo.GetRPCInfo(ctx); info != nil && info.Invocation() != nil {
		if businessError := info.Invocation().BizStatusErr(); businessError != nil {
			return "business_error", strconv.FormatInt(int64(businessError.BizStatusCode()), 10)
		}
	}
	if errors.Is(err, circuit.ErrOpen) {
		return "circuit_open", "open"
	}
	if err != nil {
		return "error", "transport"
	}
	return "ok", "0"
}

func kitexRPCDetails(ctx context.Context) (string, string) {
	service, method := "unknown", "unknown"
	info := rpcinfo.GetRPCInfo(ctx)
	if info == nil {
		return service, method
	}
	if invocation := info.Invocation(); invocation != nil {
		if invocation.ServiceName() != "" {
			service = invocation.ServiceName()
		}
		if invocation.MethodName() != "" {
			method = invocation.MethodName()
		}
	}
	if service == "unknown" && info.To() != nil && info.To().ServiceName() != "" {
		service = info.To().ServiceName()
	}
	return service, method
}

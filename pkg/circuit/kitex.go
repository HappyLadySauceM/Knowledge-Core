package circuit

import (
	"context"

	"github.com/cloudwego/kitex/pkg/endpoint"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

type StateObserver func(State)

// KitexClientMiddleware rejects outbound RPC while the breaker is open.
// KitexClientMiddleware 在熔断打开时拒绝出站 RPC，避免继续拨号。
//
// Transport and timeout errors trip the breaker; business status codes do not.
// 传输与超时错误计入失败；业务状态码不计入失败。
func KitexClientMiddleware(breaker *Breaker, observe StateObserver) endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, request, response any) error {
			if err := breaker.Allow(); err != nil {
				reportState(breaker, observe)
				return err
			}
			err := next(ctx, request, response)
			if shouldTrip(err) {
				breaker.Failure()
			} else {
				breaker.Success()
			}
			reportState(breaker, observe)
			return err
		}
	}
}

func shouldTrip(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := kerrors.FromBizStatusError(err); ok {
		return false
	}
	return true
}

func reportState(breaker *Breaker, observe StateObserver) {
	if observe == nil || breaker == nil {
		return
	}
	observe(breaker.State())
}

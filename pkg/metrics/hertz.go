package metrics

import (
	"context"
	"strconv"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

type HertzIgnoreFunc func(context.Context, *app.RequestContext) bool

func HertzServerMiddleware(registry *Registry, ignore HertzIgnoreFunc) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		if registry == nil || (ignore != nil && ignore(ctx, request)) {
			request.Next(ctx)
			return
		}

		started := time.Now()
		registry.httpInFlight.Inc()
		defer registry.httpInFlight.Dec()

		request.Next(ctx)

		method := string(request.Method())
		if method == "" {
			method = "unknown"
		}
		route := request.FullPath()
		if route == "" {
			route = "unmatched"
		}
		statusCode := strconv.Itoa(request.Response.StatusCode())
		registry.httpRequests.WithLabelValues(method, route, statusCode).Inc()
		registry.httpDuration.WithLabelValues(method, route, statusCode).Observe(time.Since(started).Seconds())
	}
}

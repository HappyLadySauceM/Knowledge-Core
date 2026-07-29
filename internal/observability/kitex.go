package observability

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/bytedance/gopkg/cloud/metainfo"
	"github.com/cloudwego/kitex/pkg/endpoint"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const requestIDMetadataKey = "x-request-id"

func KitexClientMiddleware(runtime *Runtime) endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req, resp interface{}) error {
			if RequestID(ctx) == "" {
				ctx = WithRequestID(ctx, NewRequestID())
			}
			service, method := rpcDetails(ctx)
			ctx, span := runtime.Tracer("knowledge-core/kitex/client").Start(
				ctx,
				service+"/"+method,
				trace.WithSpanKind(trace.SpanKindClient),
				trace.WithAttributes(rpcAttributes(service, method)...),
			)
			span.SetAttributes(attribute.String("request.id", RequestID(ctx)))
			defer span.End()
			ctx = injectRPCMetadata(ctx)
			started := time.Now()
			err := next(ctx, req, resp)
			finishRPC(runtime.Logger(), ctx, span, "client", service, method, started, err)
			return err
		}
	}
}

func KitexServerMiddleware(runtime *Runtime) endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req, resp interface{}) error {
			ctx = extractRPCMetadata(ctx)
			service, method := rpcDetails(ctx)
			ctx, span := runtime.Tracer("knowledge-core/kitex/server").Start(
				ctx,
				service+"/"+method,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(rpcAttributes(service, method)...),
			)
			span.SetAttributes(attribute.String("request.id", RequestID(ctx)))
			defer span.End()
			started := time.Now()
			err := next(ctx, req, resp)
			finishRPC(runtime.Logger(), ctx, span, "server", service, method, started, err)
			return err
		}
	}
}

func injectRPCMetadata(ctx context.Context) context.Context {
	ctx = metainfo.WithPersistentValue(ctx, requestIDMetadataKey, RequestID(ctx))
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	for key, value := range carrier {
		ctx = metainfo.WithPersistentValue(ctx, key, value)
	}
	return ctx
}

func extractRPCMetadata(ctx context.Context) context.Context {
	requestID, _ := metainfo.GetPersistentValue(ctx, requestIDMetadataKey)
	if requestID == "" {
		requestID = NewRequestID()
	}
	ctx = WithRequestID(ctx, requestID)
	carrier := propagation.MapCarrier{}
	for _, key := range otel.GetTextMapPropagator().Fields() {
		if value, exists := metainfo.GetPersistentValue(ctx, key); exists {
			carrier.Set(key, value)
		}
	}
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}

func rpcDetails(ctx context.Context) (service, method string) {
	service, method = "unknown", "unknown"
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

func rpcAttributes(service, method string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("rpc.system", "kitex"),
		attribute.String("rpc.service", service),
		attribute.String("rpc.method", method),
	}
}

func finishRPC(logger *slog.Logger, ctx context.Context, span trace.Span, role, service, method string, started time.Time, err error) {
	level := slog.LevelInfo
	outcome := "success"
	attrs := []slog.Attr{
		slog.String("component", "kitex"),
		slog.String("event", "rpc_request"),
		slog.String("rpc_role", role),
		slog.String("rpc_service", service),
		slog.String("rpc_method", method),
		slog.Int64("duration_ms", time.Since(started).Milliseconds()),
	}
	if err != nil {
		outcome = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, "RPC request failed")
		if bizError, ok := kerrors.FromBizStatusError(err); ok {
			level = slog.LevelWarn
			attrs = append(attrs, slog.String("biz_code", strconv.FormatInt(int64(bizError.BizStatusCode()), 10)))
		} else {
			level = slog.LevelError
			attrs = append(attrs, slog.String("error", err.Error()))
		}
	}
	attrs = append(attrs, slog.String("outcome", outcome))
	logger.LogAttrs(ctx, level, fmt.Sprintf("Kitex %s request", role), attrs...)
}

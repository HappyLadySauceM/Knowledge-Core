package observability

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/apperror"
	"github.com/HappyLadySauce/Knowledge-Core/internal/rpcerror"
	"github.com/bytedance/gopkg/cloud/metainfo"
	kitexclient "github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/endpoint"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/pkg/transmeta"
	"github.com/cloudwego/kitex/server"
	"github.com/cloudwego/kitex/transport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const requestIDMetadataKey = "x-request-id"

// KitexClientOptions keeps metadata, business errors, timeouts, and tracing on
// one wire contract. TTHeader and its meta handler must always be configured
// together; framed Thrift cannot carry these fields.
func KitexClientOptions(runtime *Runtime) []kitexclient.Option {
	return []kitexclient.Option{
		kitexclient.WithTransportProtocol(transport.TTHeader),
		kitexclient.WithMetaHandler(transmeta.ClientTTHeaderHandler),
		kitexclient.WithMiddleware(KitexClientMiddleware(runtime)),
	}
}

func KitexServerOptions(runtime *Runtime) []server.Option {
	return []server.Option{
		server.WithMetaHandler(transmeta.ServerTTHeaderHandler),
		server.WithEnableContextTimeout(true),
		server.WithMiddleware(KitexServerMiddleware(runtime)),
	}
}

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
			finishRPC(runtime.Logger(), ctx, span, "client", service, method, started, rpcOutcomeError(ctx, err))
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
			finishRPC(runtime.Logger(), ctx, span, "server", service, method, started, rpcOutcomeError(ctx, err))
			return err
		}
	}
}

// Kitex turns business errors into invocation metadata before control returns
// through middleware. Read that status so access logs and spans do not report
// a failed RPC as successful.
func rpcOutcomeError(ctx context.Context, err error) error {
	if err != nil {
		return err
	}
	info := rpcinfo.GetRPCInfo(ctx)
	if info == nil || info.Invocation() == nil {
		return nil
	}
	return info.Invocation().BizStatusErr()
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
			key, kind := rpcerror.Metadata(err)
			level = applicationErrorLevel(kind)
			attrs = append(attrs,
				slog.String("error_code", strconv.FormatInt(int64(bizError.BizStatusCode()), 10)),
			)
			if key != "" {
				attrs = append(attrs, slog.String("error_key", key))
			}
			if kind != "" {
				attrs = append(attrs, slog.String("error_kind", string(kind)))
			}
			if level == slog.LevelError {
				if cause := apperror.Cause(err); cause != nil {
					attrs = append(attrs, slog.Any("error", cause))
				}
			}
		} else {
			level = slog.LevelError
			attrs = append(attrs, slog.Any("error", err))
		}
	}
	attrs = append(attrs, slog.String("outcome", outcome))
	logger.LogAttrs(ctx, level, fmt.Sprintf("Kitex %s request", role), attrs...)
}

func applicationErrorLevel(kind apperror.Kind) slog.Level {
	switch kind {
	case apperror.KindInvalidArgument, apperror.KindNotFound:
		return slog.LevelInfo
	case apperror.KindDeadlineExceeded, apperror.KindUnavailable, apperror.KindInternal:
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}

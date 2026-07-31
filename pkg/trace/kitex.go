package trace

import (
	"context"
	"strings"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/bytedance/gopkg/cloud/metainfo"
	kitexclient "github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/endpoint"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/pkg/transmeta"
	"github.com/cloudwego/kitex/server"
	"github.com/cloudwego/kitex/transport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const RequestIDMetadataKey = "x-request-id"

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
		return func(ctx context.Context, req, resp any) error {
			ctx = metadata.EnsureRequestID(ctx)
			service, method := rpcDetails(ctx)
			ctx, span := tracer(runtime, "knowledge-core/kitex/client").Start(
				ctx,
				service+"/"+method,
				oteltrace.WithSpanKind(oteltrace.SpanKindClient),
				oteltrace.WithAttributes(rpcAttributes(service, method)...),
			)
			span.SetAttributes(attribute.String("request.id", metadata.RequestID(ctx)))
			defer span.End()
			ctx = injectRPCMetadata(ctx)
			err := next(ctx, req, resp)
			finishRPCSpan(ctx, span, rpcOutcomeError(ctx, err))
			return err
		}
	}
}

func KitexServerMiddleware(runtime *Runtime) endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req, resp any) error {
			ctx = extractRPCMetadata(ctx)
			service, method := rpcDetails(ctx)
			ctx, span := tracer(runtime, "knowledge-core/kitex/server").Start(
				ctx,
				service+"/"+method,
				oteltrace.WithSpanKind(oteltrace.SpanKindServer),
				oteltrace.WithAttributes(rpcAttributes(service, method)...),
			)
			span.SetAttributes(attribute.String("request.id", metadata.RequestID(ctx)))
			defer span.End()
			err := next(ctx, req, resp)
			finishRPCSpan(ctx, span, rpcOutcomeError(ctx, err))
			return err
		}
	}
}

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
	ctx = metainfo.WithPersistentValue(ctx, RequestIDMetadataKey, metadata.RequestID(ctx))
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	for key, value := range carrier {
		ctx = metainfo.WithPersistentValue(ctx, key, value)
	}
	return ctx
}

func extractRPCMetadata(ctx context.Context) context.Context {
	requestID, _ := metainfo.GetPersistentValue(ctx, RequestIDMetadataKey)
	ctx = metadata.WithRequestID(ctx, strings.TrimSpace(requestID))
	ctx = metadata.EnsureRequestID(ctx)
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

func finishRPCSpan(ctx context.Context, span oteltrace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, "RPC request failed")
	if requestID := metadata.RequestID(ctx); requestID != "" {
		span.SetAttributes(attribute.String("request.id", requestID))
	}
}

package observability

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/apperror"
	"github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	"github.com/HappyLadySauce/Knowledge-Core/internal/rpcerror"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/platform/platformservice"
	kitexclient "github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/remote/trans/gonet"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/server"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestKitexOptionsPreserveMetadataAndBusinessErrorsOverTCP(t *testing.T) {
	runtime, recorder := newRecordedRuntime(t)
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() { otel.SetTextMapPropagator(previousPropagator) })

	definition := apperror.MustDefine(
		"observability.wire_test",
		apperror.KindInternal,
		"internal service error",
	)
	privateCause := errors.New("private database detail")
	captures := make(chan wireCapture, 1)
	handler := &wireHandler{
		captures: captures,
		err:      rpcerror.New(49001, definition, privateCause),
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serverOptions := []server.Option{
		server.WithListener(listener),
		server.WithServerBasicInfo(&rpcinfo.EndpointBasicInfo{ServiceName: "knowledge-core.platform.wire-test"}),
		server.WithTransServerFactory(gonet.NewTransServerFactory()),
		server.WithTransHandlerFactory(gonet.NewSvrTransHandlerFactory()),
	}
	exitSignal := make(chan error, 1)
	serverOptions = append(serverOptions, server.WithExitSignal(func() <-chan error { return exitSignal }))
	serverOptions = append(serverOptions, KitexServerOptions(runtime)...)
	rpcServer := platformservice.NewServer(handler, serverOptions...)
	serverResult := make(chan error, 1)
	go func() { serverResult <- rpcServer.Run() }()
	t.Cleanup(func() {
		exitSignal <- nil
		select {
		case runErr := <-serverResult:
			if runErr != nil {
				t.Errorf("wire server Run() error = %v", runErr)
			}
		case <-time.After(3 * time.Second):
			t.Error("wire server did not stop")
		}
	})

	const rpcTimeout = 2 * time.Second
	clientOptions := []kitexclient.Option{
		kitexclient.WithHostPorts(listener.Addr().String()),
		kitexclient.WithConnectTimeout(time.Second),
		kitexclient.WithRPCTimeout(rpcTimeout),
		kitexclient.WithShortConnection(),
		kitexclient.WithDialer(gonet.NewDialer()),
		kitexclient.WithTransHandlerFactory(gonet.NewCliTransHandlerFactory()),
	}
	clientOptions = append(clientOptions, KitexClientOptions(runtime)...)
	client, err := platformservice.NewClient("knowledge-core.platform.wire-test", clientOptions...)
	if err != nil {
		t.Fatalf("create wire client: %v", err)
	}

	rootCtx, rootSpan := runtime.Tracer("wire-test").Start(context.Background(), "HTTP GET")
	rootCtx = WithRequestID(rootCtx, "request-over-wire")
	rootCtx = auth.WithAccessToken(rootCtx, "wire-access-token")
	callCtx, cancel := context.WithTimeout(rootCtx, 5*time.Second)
	defer cancel()
	callStarted := time.Now()
	_, err = client.Ping(callCtx, &common.PingRequest{})
	rootSpan.End()
	if err == nil {
		t.Fatal("Ping() error = nil, want business error")
	}

	var captured wireCapture
	select {
	case captured = <-captures:
	case <-time.After(time.Second):
		t.Fatal("wire handler was not called")
	}
	if captured.accessToken != "wire-access-token" {
		t.Fatalf("server access token = %q", captured.accessToken)
	}
	if captured.requestID != "request-over-wire" {
		t.Fatalf("server request ID = %q", captured.requestID)
	}
	if captured.spanContext.TraceID() != rootSpan.SpanContext().TraceID() {
		t.Fatalf("trace IDs = root %s, server %s", rootSpan.SpanContext().TraceID(), captured.spanContext.TraceID())
	}
	if !captured.hasDeadline {
		t.Fatal("server context has no deadline")
	}
	deadlineDelay := captured.deadline.Sub(callStarted)
	if deadlineDelay < rpcTimeout-time.Second || deadlineDelay > rpcTimeout+time.Second {
		t.Fatalf("server deadline delay = %s, want about %s", deadlineDelay, rpcTimeout)
	}

	bizError, ok := kerrors.FromBizStatusError(err)
	if !ok {
		t.Fatalf("Ping() error = %T %v, want BizStatusError", err, err)
	}
	if bizError.BizStatusCode() != 49001 || bizError.BizMessage() != definition.SafeMessage() {
		t.Fatalf("wire business error = code %d, message %q", bizError.BizStatusCode(), bizError.BizMessage())
	}
	key, kind := rpcerror.Metadata(err)
	if key != definition.Key() || kind != definition.Kind() {
		t.Fatalf("wire business metadata = key %q, kind %q", key, kind)
	}
	if strings.Contains(err.Error(), privateCause.Error()) {
		t.Fatalf("wire error leaked private cause: %v", err)
	}

	var clientSpanContext trace.SpanContext
	var serverParent trace.SpanContext
	clientStatus, serverStatus := codes.Unset, codes.Unset
	for _, span := range recorder.Ended() {
		switch span.SpanKind() {
		case trace.SpanKindClient:
			clientSpanContext = span.SpanContext()
			clientStatus = span.Status().Code
		case trace.SpanKindServer:
			if span.SpanContext().TraceID() == captured.spanContext.TraceID() &&
				span.SpanContext().SpanID() == captured.spanContext.SpanID() {
				serverParent = span.Parent()
				serverStatus = span.Status().Code
			}
		}
	}
	if !clientSpanContext.IsValid() || serverParent.SpanID() != clientSpanContext.SpanID() || !serverParent.IsRemote() {
		t.Fatalf(
			"server parent = %s (remote %t), client span = %s",
			serverParent.SpanID(),
			serverParent.IsRemote(),
			clientSpanContext.SpanID(),
		)
	}
	if clientStatus != codes.Error || serverStatus != codes.Error {
		t.Fatalf("span status = client %s, server %s; want Error", clientStatus, serverStatus)
	}
}

type wireCapture struct {
	accessToken string
	requestID   string
	spanContext trace.SpanContext
	deadline    time.Time
	hasDeadline bool
}

type wireHandler struct {
	captures chan<- wireCapture
	err      error
}

func (h *wireHandler) Ping(ctx context.Context, _ *common.PingRequest) (*common.PingResponse, error) {
	deadline, hasDeadline := ctx.Deadline()
	h.captures <- wireCapture{
		accessToken: auth.AccessToken(ctx),
		requestID:   RequestID(ctx),
		spanContext: trace.SpanContextFromContext(ctx),
		deadline:    deadline,
		hasDeadline: hasDeadline,
	}
	return nil, h.err
}

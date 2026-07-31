package trace

import (
	"context"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/cloudwego/hertz/pkg/app"
)

func TestHertzIgnoredSpanStillPropagatesRequestMetadata(t *testing.T) {
	request := app.NewContext(0)
	request.Request.Header.Set(RequestIDHeader, "request-from-client")
	var receivedRequestID string
	request.SetHandlers(app.HandlersChain{
		HertzServerMiddleware(nil, func(context.Context, *app.RequestContext) bool { return true }),
		func(ctx context.Context, _ *app.RequestContext) {
			receivedRequestID = metadata.RequestID(ctx)
		},
	})

	request.Next(context.Background())
	if receivedRequestID != "request-from-client" {
		t.Fatalf("request ID in handler context = %q", receivedRequestID)
	}
	if got := string(request.Response.Header.Peek(RequestIDHeader)); got != "request-from-client" {
		t.Fatalf("response request ID = %q", got)
	}
}

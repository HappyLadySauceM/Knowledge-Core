package middleware

import (
	"context"
	"encoding/json"
	"testing"

	collaborationv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/collaboration"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/circuit"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

func TestCollaborationDeadlineMapsToGatewayTimeout(t *testing.T) {
	request := app.NewContext(0)
	error := kerrors.NewBizStatusErrorWithExtra(
		collaborationv1.CodeUnavailable,
		"request deadline exceeded",
		map[string]string{
			apperror.ExtraErrorKey:  "collaboration.deadline_exceeded",
			apperror.ExtraErrorKind: string(apperror.KindDeadlineExceeded),
		},
	)

	WriteCollaborationError(context.Background(), request, error)

	if status := request.Response.StatusCode(); status != consts.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", status, consts.StatusGatewayTimeout)
	}
}

func TestCircuitOpenMapsToDependencyUnavailable(t *testing.T) {
	writers := []struct {
		name  string
		write func(context.Context, *app.RequestContext, error)
	}{
		{name: "identity", write: WriteIdentityError},
		{name: "knowledge", write: WriteKnowledgeError},
		{name: "collaboration", write: WriteCollaborationError},
	}
	for _, writer := range writers {
		t.Run(writer.name, func(t *testing.T) {
			request := app.NewContext(0)
			writer.write(context.Background(), request, circuit.ErrOpen)
			if status := request.Response.StatusCode(); status != consts.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", status, consts.StatusServiceUnavailable)
			}
			var problem apperror.HTTPProblem
			if err := json.Unmarshal(request.Response.Body(), &problem); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if problem.Key != "gateway.dependency_unavailable" {
				t.Fatalf("key = %q, want gateway.dependency_unavailable", problem.Key)
			}
		})
	}
}

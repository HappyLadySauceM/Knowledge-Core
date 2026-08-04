package middleware

import (
	"context"
	"testing"

	collaborationv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/collaboration"
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

package apperror_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func TestWriteHertzErrorCarriesSafeMetadata(t *testing.T) {
	definition := apperror.MustDefine(20002, "identity.conflict", apperror.KindConflict, "identity already exists")
	ctx := metadata.WithRequestID(context.Background(), "request-123")
	request := app.NewContext(0)

	apperror.WriteHertzError(ctx, request, definition.Wrap(errors.New("duplicate private@example.com")))
	if request.Response.StatusCode() != consts.StatusConflict {
		t.Fatalf("status = %d", request.Response.StatusCode())
	}
	body := string(request.Response.Body())
	if !strings.Contains(body, `"request_id":"request-123"`) || strings.Contains(body, "private@example.com") {
		t.Fatalf("response body = %s", body)
	}
	if value := request.Response.Header.Get("X-Request-ID"); value != "request-123" {
		t.Fatalf("X-Request-ID = %q", value)
	}
}

func TestToHTTPErrorHidesUnknownErrors(t *testing.T) {
	status, response := apperror.ToHTTPError(context.Background(), errors.New("private database failure"))
	if status != consts.StatusInternalServerError || response.Error.Key != apperror.Internal.Key {
		t.Fatalf("ToHTTPError() = %d, %#v", status, response)
	}
	if strings.Contains(response.Error.Message, "database") {
		t.Fatalf("unsafe response = %#v", response)
	}
}

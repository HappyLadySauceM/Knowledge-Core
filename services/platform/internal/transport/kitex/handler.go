package kitex

import (
	"context"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/health"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
)

type Handler struct {
	health *health.Registry
}

func NewHandler(registry *health.Registry) *Handler { return &Handler{health: registry} }

func (h *Handler) Ping(ctx context.Context, _ *common.PingRequest) (*common.PingResponse, error) {
	status := "not_ready"
	if h != nil && h.health != nil && h.health.Ready(ctx) == nil {
		status = "ok"
	}
	return &common.PingResponse{
		Service:  "platform",
		Status:   status,
		UnixTime: time.Now().UTC().Unix(),
	}, nil
}

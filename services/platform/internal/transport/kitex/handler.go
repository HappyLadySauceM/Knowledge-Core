package kitex

import (
	"context"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) Ping(context.Context, *common.PingRequest) (*common.PingResponse, error) {
	return &common.PingResponse{
		Service:  "platform",
		Status:   "ok",
		UnixTime: time.Now().UTC().Unix(),
	}, nil
}

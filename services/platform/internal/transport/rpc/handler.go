package rpc

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"strings"
	"time"

	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	platformv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/platform"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/domain"
	platformerrors "github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/service"
)

type Readiness interface{ Ready(context.Context) error }

type Handler struct {
	service       *service.Service
	verifier      *coreauth.Verifier
	readiness     Readiness
	logger        *slog.Logger
	internalToken string
	now           func() time.Time
}

func NewHandler(service *service.Service, verifier *coreauth.Verifier, readiness Readiness, logger *slog.Logger, internalToken string) (*Handler, error) {
	if service == nil || verifier == nil || readiness == nil || logger == nil || strings.TrimSpace(internalToken) == "" {
		return nil, errors.New("platform RPC handler dependencies are required")
	}
	return &Handler{service: service, verifier: verifier, readiness: readiness, logger: logger, internalToken: strings.TrimSpace(internalToken), now: time.Now}, nil
}

func (h *Handler) Ping(ctx context.Context, _ *commonv1.PingRequest) (*commonv1.PingResponse, error) {
	ctx = metadata.EnsureRequestID(ctx)
	status := "ready"
	if err := h.readiness.Ready(ctx); err != nil {
		status = "not_ready"
	}
	return &commonv1.PingResponse{Service: "platform", Status: status, UnixTime: h.now().UTC().Unix()}, nil
}

func (h *Handler) Live(context.Context, *commonv1.PingRequest) (*commonv1.PingResponse, error) {
	return &commonv1.PingResponse{Service: "platform", Status: "live", UnixTime: h.now().UTC().Unix()}, nil
}

func (h *Handler) GetSiteProfile(ctx context.Context, request *commonv1.EmptyResponse) (*platformv1.SiteProfile, error) {
	if request == nil {
		return nil, apperror.ToKitexBizStatus(ctx, platformerrors.InvalidInput.New())
	}
	profile, err := h.service.SiteProfile(ctx)
	if err != nil {
		return nil, h.mapError(ctx, err)
	}
	return profile, nil
}

func (h *Handler) GetConfiguration(ctx context.Context, request *platformv1.GetConfigurationRequest) (*platformv1.Configuration, error) {
	if _, err := h.admin(ctx, request != nil); err != nil {
		return nil, err
	}
	configuration, err := h.service.Get(ctx, request.Namespace)
	if err != nil {
		return nil, h.mapError(ctx, err)
	}
	return configuration, nil
}

func (h *Handler) PutConfiguration(ctx context.Context, request *platformv1.PutConfigurationRequest) (*platformv1.Configuration, error) {
	actorID, err := h.admin(ctx, request != nil)
	if err != nil {
		return nil, err
	}
	configuration, err := h.service.Put(ctx, actorID, request)
	if err != nil {
		return nil, h.mapError(ctx, err)
	}
	return configuration, nil
}

func (h *Handler) GetConfigurationDelivery(ctx context.Context, request *platformv1.GetConfigurationDeliveryRequest) (*platformv1.ConfigurationDelivery, error) {
	if _, err := h.admin(ctx, request != nil); err != nil {
		return nil, err
	}
	delivery, err := h.service.Delivery(ctx, request.Namespace, request.Revision)
	if err != nil {
		return nil, h.mapError(ctx, err)
	}
	return delivery, nil
}

func (h *Handler) GetConsumerConfiguration(ctx context.Context, request *platformv1.GetConsumerConfigurationRequest) (*platformv1.Configuration, error) {
	if err := h.internal(ctx, request != nil); err != nil {
		return nil, err
	}
	configuration, err := h.service.ConsumerConfiguration(ctx, request.Namespace, request.Revision, request.Consumer)
	if err != nil {
		return nil, h.mapError(ctx, err)
	}
	return configuration, nil
}

func (h *Handler) GetConsumerState(ctx context.Context, request *platformv1.GetConsumerStateRequest) (*platformv1.ConsumerConfigurationState, error) {
	if err := h.internal(ctx, request != nil); err != nil {
		return nil, err
	}
	state, err := h.service.ConsumerState(ctx, request.Namespace, request.Consumer)
	if err != nil {
		return nil, h.mapError(ctx, err)
	}
	return state, nil
}

func (h *Handler) ReportConfigurationApply(ctx context.Context, request *platformv1.ReportConfigurationApplyRequest) (*commonv1.EmptyResponse, error) {
	if err := h.internal(ctx, request != nil); err != nil {
		return nil, err
	}
	if err := h.service.ReportConsumerApply(ctx, request); err != nil {
		return nil, h.mapError(ctx, err)
	}
	return &commonv1.EmptyResponse{}, nil
}

func (h *Handler) admin(ctx context.Context, valid bool) (int64, error) {
	if !valid {
		return 0, apperror.ToKitexBizStatus(ctx, platformerrors.InvalidInput.New())
	}
	token := strings.TrimSpace(coreauth.AccessToken(ctx))
	if token == "" {
		return 0, apperror.ToKitexBizStatus(ctx, platformerrors.Unauthenticated.New())
	}
	principal, err := h.verifier.Verify(token)
	if err != nil || principal.UserID <= 0 {
		return 0, apperror.ToKitexBizStatus(ctx, platformerrors.Unauthenticated.Wrap(err))
	}
	if principal.Role != "admin" {
		return 0, apperror.ToKitexBizStatus(ctx, platformerrors.Forbidden.New())
	}
	return principal.UserID, nil
}

func (h *Handler) internal(ctx context.Context, valid bool) error {
	if !valid {
		return apperror.ToKitexBizStatus(ctx, platformerrors.InvalidInput.New())
	}
	provided := coreauth.ServiceToken(ctx)
	if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(h.internalToken)) != 1 {
		return apperror.ToKitexBizStatus(ctx, platformerrors.Unauthenticated.New())
	}
	return nil
}

func (h *Handler) mapError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, domain.ErrInvalid):
		err = platformerrors.InvalidInput.Wrap(err)
	case errors.Is(err, domain.ErrNotFound):
		err = platformerrors.NotFound.Wrap(err)
	case errors.Is(err, domain.ErrPrecondition):
		err = platformerrors.PreconditionFailed.Wrap(err)
	case errors.Is(err, domain.ErrConflict):
		err = platformerrors.Conflict.Wrap(err)
	default:
		if _, ok := apperror.Details(err); !ok {
			err = platformerrors.Internal.Wrap(err)
		}
	}
	return apperror.ToKitexBizStatus(ctx, err)
}

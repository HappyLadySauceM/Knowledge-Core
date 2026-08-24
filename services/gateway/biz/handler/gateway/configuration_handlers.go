package gateway

import (
	"context"
	"strconv"

	platformv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/platform"
	gatewaymodel "github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/model/gateway"
	gatewaymiddleware "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func handleGetConfiguration(ctx context.Context, request *app.RequestContext) {
	namespace, namespaceErr := configurationNamespace(request)
	if namespaceErr != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok || dependencies.Platform == nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	configuration, err := dependencies.Platform.GetConfiguration(upstreamContext(ctx, request), &platformv1.GetConfigurationRequest{Namespace: namespace})
	if err != nil {
		gatewaymiddleware.WritePlatformError(ctx, request, err)
		return
	}
	if configuration == nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	data := toConfigurationData(configuration)
	request.Header("ETag", formatETag(data.Revision))
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handlePutConfiguration(ctx context.Context, request *app.RequestContext) {
	namespace, namespaceErr := configurationNamespace(request)
	revision, revisionErr := expectedConfigurationRevision(request)
	idempotency, idempotencyErr := idempotencyKey(request)
	var body putConfigurationBody
	if namespaceErr != nil || revisionErr != nil || idempotencyErr != nil || requireNoQuery(request) != nil || decodeJSONBody(request, &body) != nil || len(body.Values) == 0 || len(body.Values) > 32 {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok || dependencies.Platform == nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	configuration, err := dependencies.Platform.PutConfiguration(upstreamContext(ctx, request), &platformv1.PutConfigurationRequest{Namespace: namespace, ExpectedRevision: revision, IdempotencyKey: idempotency, Values: body.Values})
	if err != nil {
		gatewaymiddleware.WritePlatformError(ctx, request, err)
		return
	}
	if configuration == nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	data := toConfigurationData(configuration)
	request.Header("ETag", formatETag(data.Revision))
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleGetConfigurationDelivery(ctx context.Context, request *app.RequestContext) {
	namespace, namespaceErr := configurationNamespace(request)
	revision, revisionErr := configurationRevision(request)
	if namespaceErr != nil || revisionErr != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok || dependencies.Platform == nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	delivery, err := dependencies.Platform.GetConfigurationDelivery(upstreamContext(ctx, request), &platformv1.GetConfigurationDeliveryRequest{Namespace: namespace, Revision: revision})
	if err != nil {
		gatewaymiddleware.WritePlatformError(ctx, request, err)
		return
	}
	if delivery == nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, &gatewaymodel.ConfigurationDeliveryData{MessageID: delivery.MessageId, Namespace: delivery.Namespace, Revision: delivery.Revision, Status: delivery.Status, Attempts: delivery.Attempts, LastErrorKey: delivery.LastErrorKey, PublishedAt: delivery.PublishedAt})
}

func toConfigurationData(configuration *platformv1.Configuration) *gatewaymodel.ConfigurationData {
	values := make([]*gatewaymodel.ConfigurationValueData, 0, len(configuration.Values))
	for _, value := range configuration.Values {
		if value == nil {
			continue
		}
		values = append(values, &gatewaymodel.ConfigurationValueData{Key: value.Key, Value: value.Value, Secret: value.Secret, Redacted: value.Redacted})
	}
	return &gatewaymodel.ConfigurationData{Environment: configuration.Environment, Namespace: configuration.Namespace, Revision: configuration.Revision, SchemaVersion: configuration.SchemaVersion, Values: values, UpdatedAt: configuration.UpdatedAt, UpdatedBy: strconv.FormatInt(configuration.UpdatedBy, 10)}
}

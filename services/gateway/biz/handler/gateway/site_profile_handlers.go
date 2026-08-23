package gateway

import (
	"context"
	"strings"

	gatewaymodel "github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/model/gateway"
	gatewaymiddleware "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func handleGetSiteProfile(ctx context.Context, request *app.RequestContext) {
	if requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	options := dependencies.EndpointOptions()
	if strings.TrimSpace(options.SiteTitle) == "" || strings.TrimSpace(options.SiteHeroImageURL) == "" {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, &gatewaymodel.SiteProfileData{
		Title: options.SiteTitle, TaglineZh: options.SiteTaglineZH, TaglineEn: options.SiteTaglineEN,
		HeroImageURL: options.SiteHeroImageURL, HeroFocalX: options.SiteHeroFocalX, HeroFocalY: options.SiteHeroFocalY, Revision: 1,
	})
}

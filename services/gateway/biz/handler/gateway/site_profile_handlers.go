package gateway

import (
	"context"
	"strings"

	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	platformv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/platform"
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
	if dependencies.Platform != nil {
		profile, err := dependencies.Platform.GetSiteProfile(upstreamContext(ctx, request), &commonv1.EmptyResponse{})
		if err == nil && profile != nil {
			writeJSON(ctx, request, consts.StatusOK, toSiteProfileData(profile))
			return
		}
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

func toSiteProfileData(profile *platformv1.SiteProfile) *gatewaymodel.SiteProfileData {
	return &gatewaymodel.SiteProfileData{
		Title: profile.Title, TaglineZh: profile.TaglineZh, TaglineEn: profile.TaglineEn,
		HeroImageURL: profile.HeroImageUrl, HeroFocalX: profile.HeroFocalX, HeroFocalY: profile.HeroFocalY,
		Revision: profile.Revision, HeroAttachmentID: profile.HeroAttachmentId,
	}
}

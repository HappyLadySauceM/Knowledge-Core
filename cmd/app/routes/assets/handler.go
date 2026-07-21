package assets

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"

	"github.com/HappyLadySauce/Knowledge-Core/cmd/app/middleware"
	"github.com/HappyLadySauce/Knowledge-Core/cmd/app/router"
	"github.com/HappyLadySauce/Knowledge-Core/cmd/app/svc"
	"github.com/HappyLadySauce/Knowledge-Core/cmd/app/types/common"
	v1 "github.com/HappyLadySauce/Knowledge-Core/cmd/app/types/v1"
	internalassets "github.com/HappyLadySauce/Knowledge-Core/internal/assets"
	apperrors "github.com/HappyLadySauce/Knowledge-Core/internal/errors"
)

const uploadMultipartOverhead = 1 << 20

type Controller struct {
	service *internalassets.Service
	maxSize int64
}

func Init(ctx context.Context, sc *svc.ServiceContext) error {
	_ = ctx
	if sc.Assets == nil || sc.Config == nil || sc.Config.Uploads == nil {
		return fmt.Errorf("asset service is not initialized")
	}
	RegisterRoutes(router.V1(), router.Router(), sc.Assets, sc, sc.Config.Uploads.MaxBytes, sc.Config.Uploads.PublicPath)
	return nil
}

func RegisterRoutes(group *gin.RouterGroup, root *gin.Engine, service *internalassets.Service, sc *svc.ServiceContext, maxSize int64, publicPath string) {
	controller := &Controller{service: service, maxSize: maxSize}
	root.GET(strings.TrimRight(publicPath, "/")+"/:id/content", controller.Content)

	adminGroup := group.Group("/admin", middleware.AuthMiddleware(sc), middleware.RequireAdmin())
	adminGroup.POST("/assets", controller.Upload)
	adminGroup.DELETE("/assets/:id", controller.Delete)
}

// Upload stores an image uploaded by an administrator.
// Upload 保存管理员上传的图片。
// @Summary Upload image asset
// @Description Upload an image to local storage. Admin only.
// @Tags Admin Assets
// @Accept mpfd
// @Produce json
// @Security BearerAuth
// @Param file formData file true "Image file"
// @Success 201 {object} common.SwaggerResponse{data=v1.AssetResponse}
// @Failure 400 {object} common.SwaggerErrorResponse
// @Failure 401 {object} common.SwaggerErrorResponse
// @Failure 403 {object} common.SwaggerErrorResponse
// @Failure 500 {object} common.SwaggerErrorResponse
// @Router /api/v1/admin/assets [post]
func (h *Controller) Upload(c *gin.Context) {
	actor, ok := middleware.UserFromContext(c)
	if !ok {
		common.Error(c, apperrors.InvalidToken)
		return
	}
	if h.maxSize <= 0 || h.maxSize > (1<<62)-uploadMultipartOverhead {
		common.Error(c, apperrors.InternalError)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxSize+uploadMultipartOverhead)
	fileHeader, err := c.FormFile("file")
	if err != nil {
		common.Error(c, apperrors.InvalidRequest)
		return
	}
	if c.Request.MultipartForm != nil {
		defer func() { _ = c.Request.MultipartForm.RemoveAll() }()
	}
	file, err := fileHeader.Open()
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer file.Close()
	asset, err := h.service.Upload(c.Request.Context(), actor, internalassets.UploadInput{
		Filename: fileHeader.Filename,
		Size:     fileHeader.Size,
		Body:     file,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	common.Created(c, toAssetResponse(h.service, asset))
}

// Content serves a ready image asset.
// Content 输出已就绪的图片资源。
// @Summary Get image asset
// @Description Serve a publicly readable image asset.
// @Tags Assets
// @Produce image/jpeg,image/png,image/webp,image/gif,image/avif
// @Param id path int true "Asset ID"
// @Success 200 {file} binary
// @Failure 400 {object} common.SwaggerErrorResponse
// @Failure 404 {object} common.SwaggerErrorResponse
// @Failure 500 {object} common.SwaggerErrorResponse
// @Router /api/v1/assets/{id}/content [get]
func (h *Controller) Content(c *gin.Context) {
	id, ok := assetIDParam(c)
	if !ok {
		return
	}
	asset, file, err := h.service.OpenPublic(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer file.Close()

	c.Header("Content-Type", asset.ContentType)
	c.Header("Content-Length", strconv.FormatInt(asset.SizeBytes, 10))
	c.Header("Content-Disposition", contentDisposition(asset.OriginalName))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("ETag", fmt.Sprintf("\"%s\"", asset.SHA256))
	http.ServeContent(c.Writer, c.Request, asset.OriginalName, asset.UpdatedAt, file)
}

// Delete soft-deletes an image asset.
// Delete 软删除图片资源。
// @Summary Delete image asset
// @Description Mark an image asset deleted. Admin only.
// @Tags Admin Assets
// @Produce json
// @Security BearerAuth
// @Param id path int true "Asset ID"
// @Success 200 {object} common.SwaggerResponse
// @Failure 400 {object} common.SwaggerErrorResponse
// @Failure 401 {object} common.SwaggerErrorResponse
// @Failure 403 {object} common.SwaggerErrorResponse
// @Failure 404 {object} common.SwaggerErrorResponse
// @Failure 500 {object} common.SwaggerErrorResponse
// @Router /api/v1/admin/assets/{id} [delete]
func (h *Controller) Delete(c *gin.Context) {
	actor, ok := middleware.UserFromContext(c)
	if !ok {
		common.Error(c, apperrors.InvalidToken)
		return
	}
	id, ok := assetIDParam(c)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), actor, id); err != nil {
		writeServiceError(c, err)
		return
	}
	common.OK[any](c, nil)
}

func toAssetResponse(service *internalassets.Service, asset internalassets.Asset) v1.AssetResponse {
	return v1.AssetResponse{
		ID:          asset.ID,
		URL:         service.PublicURL(asset),
		Filename:    asset.OriginalName,
		ContentType: asset.ContentType,
		SizeBytes:   asset.SizeBytes,
		SHA256:      asset.SHA256,
		Status:      asset.Status,
		CreatedBy:   asset.CreatedBy,
		CreatedAt:   asset.CreatedAt,
		UpdatedAt:   asset.UpdatedAt,
	}
}

func assetIDParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.Error(c, apperrors.InvalidRequest)
		return 0, false
	}
	return id, true
}

func contentDisposition(filename string) string {
	filename = strings.TrimSpace(strings.ReplaceAll(filename, "\\", "/"))
	filename = strings.TrimSpace(strings.TrimPrefix(filename, "/"))
	if filename == "" {
		filename = "asset"
	}
	value := mime.FormatMediaType("inline", map[string]string{"filename": filename})
	if value == "" {
		return "inline"
	}
	return value
}

func writeServiceError(c *gin.Context, err error) {
	appErr := apperrors.From(err)
	if appErr.Code == apperrors.CodeInternalError {
		klog.ErrorS(err, "asset request failed")
	}
	common.Error(c, appErr)
}

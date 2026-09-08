// Package apidocs serves the generated API documentation from the Gateway
// admin listener. The public listener never registers these routes.
package apidocs

import (
	"context"
	"errors"
	"io/fs"

	docsassets "github.com/HappyLadySauce/Knowledge-Core/api"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

const documentationCSP = "default-src 'none'; style-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// Register adds the generated documentation routes to an admin Hertz server.
// The explicit enabled switch keeps the feature disabled unless the operator
// opts in for the current environment.
func Register(h *server.Hertz, enabled bool) error {
	if h == nil {
		return errors.New("register API documentation: admin server is required")
	}
	if !enabled {
		return nil
	}

	h.GET("/docs", func(_ context.Context, request *app.RequestContext) {
		setDocumentationHeaders(request)
		request.Redirect(consts.StatusPermanentRedirect, []byte("/docs/"))
		request.Abort()
	})
	h.GET("/docs/", serveAsset("index.html", consts.MIMETextHtml+"; charset=utf-8"))
	h.GET("/docs/http", serveAsset("http.html", consts.MIMETextHtml+"; charset=utf-8"))
	h.GET("/docs/websocket", serveAsset("websocket.html", consts.MIMETextHtml+"; charset=utf-8"))
	h.GET("/docs/openapi.yaml", serveAsset("openapi.yaml", "application/yaml; charset=utf-8"))
	h.GET("/docs/openapi.json", serveAsset("openapi.json", consts.MIMEApplicationJSONUTF8))
	h.GET("/docs/asyncapi.yaml", serveAsset("asyncapi.yaml", "application/yaml; charset=utf-8"))
	h.GET("/docs/asyncapi.json", serveAsset("asyncapi.json", consts.MIMEApplicationJSONUTF8))
	h.GET("/docs/assets/style.css", serveAsset("style.css", consts.MIMETextCss+"; charset=utf-8"))
	return nil
}

func serveAsset(name, contentType string) app.HandlerFunc {
	return func(_ context.Context, request *app.RequestContext) {
		setDocumentationHeaders(request)
		contents, err := fs.ReadFile(docsassets.Assets, name)
		if err != nil {
			request.Data(consts.StatusInternalServerError, consts.MIMETextPlainUTF8, []byte("API documentation is unavailable"))
			request.Abort()
			return
		}
		request.Data(consts.StatusOK, contentType, contents)
		request.Abort()
	}
}

func setDocumentationHeaders(request *app.RequestContext) {
	request.Header("Content-Security-Policy", documentationCSP)
	request.Header("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
	request.Header("Referrer-Policy", "no-referrer")
	request.Header("X-Content-Type-Options", "nosniff")
	request.Header("X-Frame-Options", "DENY")
	request.Header("Cache-Control", "no-store")
}

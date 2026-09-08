// Package api contains the checked-in, generated API documentation assets.
package api

import "embed"

// Assets is embedded into the Gateway admin server so the documentation page
// does not depend on a filesystem mount or an external CDN at runtime.
//
//go:embed openapi.yaml openapi.json asyncapi.yaml asyncapi.json index.html http.html websocket.html style.css
var Assets embed.FS

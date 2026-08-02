package gateway

import "testing"

func TestSensitiveRouteMiddlewareIsInstalled(t *testing.T) {
	tests := map[string]int{
		"login":        len(_loginMw()),
		"register":     len(_registerMw()),
		"studio":       len(_studioMw()),
		"current user": len(_currentuserMw()),
	}
	for name, count := range tests {
		if count == 0 {
			t.Errorf("%s route has no security middleware", name)
		}
	}
}

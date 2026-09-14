package config

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRoutesRegistersCoreEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	Routes(r)

	registered := map[string]bool{}
	for _, route := range r.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	expected := []string{
		"GET /",
		"GET /health",
		// Internal service-to-service API (gateway / delivery)
		"GET /internal/v1/auth",
		"POST /internal/v1/credentials",
		"GET /internal/v1/outbox",
		"POST /internal/v1/outbox/:id/ack",
		// Admin API
		"POST /admin/api/login",
		"GET /admin/api/status",
		"GET /admin/api/conversations",
		// IM API
		"GET /api/v1/me",
		"POST /api/v1/group",
		"GET /api/v1/conversations",
		"POST /api/v1/conversations",
		"GET /api/v1/conversations/:conversation_uuid",
		"GET /api/v1/conversations/:conversation_uuid/messages",
		"POST /api/v1/conversations/:conversation_uuid/messages",
		"POST /api/v1/messages",
		// Storage API
		"POST /api/v1/storage",
		"GET /api/v1/storage/*key",
		"DELETE /api/v1/storage/*key",
	}

	for _, route := range expected {
		if !registered[route] {
			t.Fatalf("expected route %s to be registered, got %#v", route, registered)
		}
	}
}

func TestHealthRoutesStayUnprefixed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	HealthRoutes(r)

	registered := map[string]bool{}
	for _, route := range r.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	expected := []string{
		"GET /health",
		"GET /internal/v1/auth",
		"POST /internal/v1/credentials",
		"GET /internal/v1/outbox",
		"POST /internal/v1/outbox/:id/ack",
	}

	for _, route := range expected {
		if !registered[route] {
			t.Fatalf("expected route %s on the internal router, got %#v", route, registered)
		}
	}
}

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	implugin "github.com/daqing/airway-im-plugin"
)

type recordingHandler struct {
	lastPath string
	calls    int
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls++
	h.lastPath = r.URL.Path
	w.WriteHeader(http.StatusOK)
}

func TestPrefixHandlerStripsPrefixForPublicRoutes(t *testing.T) {
	next := &recordingHandler{}
	internal := &recordingHandler{}
	handler := prefixHandler{prefix: "/im", next: next, internal: internal}

	for path, want := range map[string]string{
		"/im":                  "/",
		"/im/api/v1/me":        "/api/v1/me",
		"/im/internal/v1/auth": "/internal/v1/auth",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(httptest.NewRecorder(), request)
		if next.lastPath != want {
			t.Fatalf("%s: next saw path %q, want %q", path, next.lastPath, want)
		}
	}
	if internal.calls != 0 {
		t.Fatalf("internal router received %d prefixed requests", internal.calls)
	}
}

func TestPrefixHandlerSendsUnprefixedRequestsToInternalRouter(t *testing.T) {
	next := &recordingHandler{}
	internal := &recordingHandler{}
	handler := prefixHandler{prefix: "/im", next: next, internal: internal}

	for _, path := range []string{"/", "/health", "/internal/v1/auth", "/imx"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(httptest.NewRecorder(), request)
		if internal.lastPath != path {
			t.Fatalf("%s: internal saw path %q", path, internal.lastPath)
		}
	}
	if next.calls != 0 {
		t.Fatalf("full router received %d unprefixed requests", next.calls)
	}
}

func TestHandlerSelectsRouterByConfiguredPrefix(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Setenv("AIRWAY_URL_PREFIX", "")
	t.Setenv("URL_PREFIX", "")
	if handler := NewApp("test", "0").Handler(); handler == nil {
		t.Fatal("no-prefix handler is nil")
	} else if _, prefixed := handler.(prefixHandler); prefixed {
		t.Fatalf("expected the bare router without a prefix, got %T", handler)
	}

	t.Setenv("URL_PREFIX", "/im")
	handler := NewApp("test", "0").Handler()
	if _, prefixed := handler.(prefixHandler); !prefixed {
		t.Fatalf("expected prefixHandler with URL_PREFIX set, got %T", handler)
	}
}

func TestCORSAnswersPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CORS())
	router.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	request := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	request.Header.Set("Origin", "https://mini-program.example.com")
	request.Header.Set("Access-Control-Request-Method", "GET")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", response.Code)
	}
	if response.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatalf("preflight missing Allow-Origin header: %v", response.Header())
	}
	if !strings.Contains(response.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("preflight must allow the Authorization header: %q", response.Header().Get("Access-Control-Allow-Headers"))
	}
}

func TestVersionMatchesVERSIONFile(t *testing.T) {
	contents, err := os.ReadFile("../../VERSION")
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	if got, want := implugin.Version(), strings.TrimSpace(string(contents)); got != want {
		t.Fatalf("Version() = %q, want %q", got, want)
	}
}

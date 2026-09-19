package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupDashboardRouter(t *testing.T) *gin.Engine {
	t.Helper()
	if _, err := cached(); err != nil {
		t.Fatalf("embedded dashboard bundle broken: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	Routes(router.Group("/"))
	return router
}

func TestPageServesHTMLShell(t *testing.T) {
	router := setupDashboardRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/im", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/im: got %d, want 200", rec.Code)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("GET /admin/im content type: got %q, want text/html", contentType)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<div id="im-admin-root"></div>`,
		`window.IM_ADMIN = { base: "" };`,
		`/admin/im/assets/app.js?v=`,
		`/admin/im/assets/app.css?v=`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /admin/im body missing %q:\n%s", want, body)
		}
	}
}

func TestPageRespectsURLPrefix(t *testing.T) {
	router := setupDashboardRouter(t)
	t.Setenv("URL_PREFIX", "/airway")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/im", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `window.IM_ADMIN = { base: "/airway" };`) {
		t.Fatalf("page did not inject the URL prefix base:\n%s", body)
	}
	if !strings.Contains(body, `href="/airway/admin/im/assets/app.css?v=`) {
		t.Fatalf("stylesheet URL missing the URL prefix:\n%s", body)
	}
}

func TestAssetServing(t *testing.T) {
	router := setupDashboardRouter(t)
	m, err := cached()
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/im/assets/app.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET app.js: got %d, want 200", rec.Code)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "text/javascript") {
		t.Fatalf("app.js content type: got %q, want text/javascript", contentType)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "no-cache" {
		t.Fatalf("app.js cache without ?v=: got %q, want no-cache", cache)
	}
	if len(rec.Body.Bytes()) == 0 {
		t.Fatal("app.js body is empty")
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/im/assets/app.js?v="+m.Hash, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET app.js?v=<hash>: got %d, want 200", rec.Code)
	}
	if cache := rec.Header().Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Fatalf("app.js cache with matching ?v=: got %q, want immutable", cache)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/im/assets/app.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET app.css: got %d, want 200", rec.Code)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "text/css") {
		t.Fatalf("app.css content type: got %q, want text/css", contentType)
	}
}

func TestAssetRejectsUnknownFiles(t *testing.T) {
	router := setupDashboardRouter(t)

	for _, path := range []string{
		"/admin/im/assets/nope.js",
		"/admin/im/assets/manifest.json",
		"/admin/im/assets/..%2F..%2Fplugin.go",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s: got %d, want 404", path, rec.Code)
		}
	}
}

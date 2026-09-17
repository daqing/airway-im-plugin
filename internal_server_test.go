package implugin

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestBootServesInternalAPIOnSeparateListener(t *testing.T) {
	t.Setenv("IM_INTERNAL_SECRET", "test-internal-secret")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	t.Setenv("IM_INTERNAL_ADDR", addr)
	if err := (Plugin{}).Boot(); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	url := fmt.Sprintf("http://%s/internal/v1/auth", addr)
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("GET /internal/v1/auth without secret: got %d, want 401", resp.StatusCode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("internal listener at %s never came up: %v", addr, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBootFailsOnOccupiedAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	t.Setenv("IM_INTERNAL_ADDR", ln.Addr().String())
	if err := (Plugin{}).Boot(); err == nil {
		t.Fatal("Boot on an occupied address: got nil error, want failure")
	}
}

func TestInternalAPINotOnPublicRouter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(Plugin{}).Routes(r.Group("/"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/auth", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /internal/v1/auth on public router: got %d, want 404", rec.Code)
	}
}

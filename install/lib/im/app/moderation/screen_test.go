package moderation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeService answers /screen the way the moderation plugin does, so the call
// is exercised against the real wire shape.
func fakeService(t *testing.T, status int, body string, seen *http.Request) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = *r
		}
		if r.URL.Path != "/api/v1/moderation/screen" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	t.Setenv("IM_MODERATION_URL", server.URL+"/api/v1/moderation")
	return server
}

func TestScreenReportsTheVerdict(t *testing.T) {
	fakeService(t, http.StatusOK,
		`{"code":0,"data":{"flagged":true,"categories":["illegal"],"words":["网络赌博"]},"message":""}`, nil)

	result, err := Screen(context.Background(), "要不要一起网络赌博")
	if err != nil {
		t.Fatalf("Screen: %v", err)
	}
	if !result.Flagged || len(result.Words) != 1 || result.Words[0] != "网络赌博" {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Categories) != 1 || result.Categories[0] != "illegal" {
		t.Fatalf("result = %#v", result)
	}
}

func TestScreenReportsContentThatPasses(t *testing.T) {
	fakeService(t, http.StatusOK, `{"code":0,"data":{"flagged":false,"categories":[],"words":[]},"message":""}`, nil)

	result, err := Screen(context.Background(), "新版本什么时候发？")
	if err != nil {
		t.Fatalf("Screen: %v", err)
	}
	if result.Flagged || len(result.Words) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestScreenSendsTheSharedSecret(t *testing.T) {
	var seen http.Request
	fakeService(t, http.StatusOK, `{"code":0,"data":{"flagged":false},"message":""}`, &seen)
	t.Setenv("MODERATION_SECRET", "test-moderation-secret")

	if _, err := Screen(context.Background(), "hello"); err != nil {
		t.Fatalf("Screen: %v", err)
	}
	if got := seen.Header.Get("X-Moderation-Secret"); got != "test-moderation-secret" {
		t.Fatalf("secret header = %q", got)
	}
}

// Every way the service can fail to answer has to reach the caller as an
// error: the send path holds the message back on one, and a verdict that
// never arrived must not read as a clean one.
func TestScreenReportsFailures(t *testing.T) {
	t.Run("business code", func(t *testing.T) {
		fakeService(t, http.StatusOK, `{"code":30003,"data":null,"message":"Could not load the word list"}`, nil)
		if _, err := Screen(context.Background(), "hello"); err == nil {
			t.Fatal("a non-zero code was read as a verdict")
		}
	})

	t.Run("server error", func(t *testing.T) {
		fakeService(t, http.StatusInternalServerError, "boom", nil)
		if _, err := Screen(context.Background(), "hello"); err == nil {
			t.Fatal("a 500 was read as a verdict")
		}
	})

	t.Run("malformed body", func(t *testing.T) {
		fakeService(t, http.StatusOK, `{"code":`, nil)
		if _, err := Screen(context.Background(), "hello"); err == nil {
			t.Fatal("a malformed body was read as a verdict")
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := server.URL
		server.Close()
		t.Setenv("IM_MODERATION_URL", url+"/api/v1/moderation")
		if _, err := Screen(context.Background(), "hello"); err == nil {
			t.Fatal("an unreachable service was read as a verdict")
		}
	})

	t.Run("timeout", func(t *testing.T) {
		slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
		}))
		t.Cleanup(slow.Close)
		t.Setenv("IM_MODERATION_URL", slow.URL+"/api/v1/moderation")

		previous := screenHTTPClient
		screenHTTPClient = &http.Client{Timeout: 20 * time.Millisecond}
		t.Cleanup(func() { screenHTTPClient = previous })

		if _, err := Screen(context.Background(), "hello"); err == nil {
			t.Fatal("a service that never answered was read as a verdict")
		}
	})
}

func TestScreenFollowsTheConfiguredURL(t *testing.T) {
	if got := moderationBaseURL(); !strings.HasPrefix(got, "http://127.0.0.1:1905/api/v1/moderation") {
		t.Fatalf("default base url = %q", got)
	}
	t.Setenv("IM_MODERATION_URL", "http://moderation.internal:9000/api/v1/moderation/")
	if got := moderationBaseURL(); got != "http://moderation.internal:9000/api/v1/moderation" {
		t.Fatalf("base url = %q", got)
	}
}

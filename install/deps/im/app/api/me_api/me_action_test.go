package me_api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daqing/airway-im-plugin/deps/im/app/auth"
	"github.com/daqing/airway-im-plugin/deps/im/app/repo"
	"github.com/gin-gonic/gin"
)

const meTestSecret = "me-test-signing-secret"

func setupMeTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	t.Setenv("IM_AUTH_SECRET", meTestSecret)
	t.Setenv("IM_AUTH_SECRET_PREVIOUS", "")

	db, err := repo.SetupDB("sqlite://:memory:")
	if err != nil {
		t.Fatalf("setup database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err = db.Exec(`CREATE TABLE users (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        uuid VARCHAR(64) NOT NULL UNIQUE,
        username VARCHAR(255) NOT NULL,
        nickname VARCHAR(255),
        avatar_url VARCHAR(2048),
        email VARCHAR(255),
        last_seen_at DATETIME,
        token_version BIGINT NOT NULL DEFAULT 1,
        created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
        updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
    )`); err != nil {
		t.Fatalf("create users: %v", err)
	}
	if _, err = db.Exec(`INSERT INTO users (uuid, username, nickname, email) VALUES (?, ?, ?, ?)`, "host-42", "octocat", "The Octocat", "octocat@example.test"); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	Routes(r.Group("/api/v1"))
	return r
}

func octocatCredential(t *testing.T) string {
	t.Helper()
	credential, err := auth.MintCredential(meTestSecret, "host-42", "octocat", "The Octocat", "", time.Hour)
	if err != nil {
		t.Fatalf("mint credential: %v", err)
	}
	return credential
}

func TestMeReturnsAuthenticatedUser(t *testing.T) {
	r := setupMeTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+octocatCredential(t))
	response := httptest.NewRecorder()
	r.ServeHTTP(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Code    int            `json:"code"`
		Data    map[string]any `json:"data"`
		Message *string        `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 0 || body.Message != nil {
		t.Fatalf("unexpected envelope: %#v", body)
	}
	if body.Data["username"] != "octocat" || body.Data["uuid"] != "host-42" || body.Data["email"] != "octocat@example.test" {
		t.Fatalf("unexpected user: %#v", body.Data)
	}
}

func TestMeAutoRegistersUnknownUuid(t *testing.T) {
	r := setupMeTestRouter(t)
	credential, err := auth.MintCredential(meTestSecret, "host-999", "newcomer", "", "", time.Hour)
	if err != nil {
		t.Fatalf("mint credential: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+credential)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, req)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"uuid":"host-999"`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestMeRejectsInvalidCredentials(t *testing.T) {
	wrongSecret, err := auth.MintCredential("not-the-configured-secret", "host-42", "octocat", "", "", time.Hour)
	if err != nil {
		t.Fatalf("mint credential: %v", err)
	}

	tests := []struct {
		name   string
		header string
	}{
		{name: "missing header"},
		{name: "wrong scheme", header: "Basic " + octocatCredential(t)},
		{name: "missing credential", header: "Bearer"},
		{name: "extra fields", header: "Bearer " + octocatCredential(t) + " extra"},
		{name: "garbage", header: "Bearer not-a-credential"},
		{name: "wrong signing secret", header: "Bearer " + wrongSecret},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := setupMeTestRouter(t)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
			if test.header != "" {
				req.Header.Set("Authorization", test.header)
			}
			response := httptest.NewRecorder()
			r.ServeHTTP(response, req)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), `"code":10001`) ||
				!strings.Contains(response.Body.String(), `"data":null`) ||
				!strings.Contains(response.Body.String(), `"message":"Invalid bearer token"`) {
				t.Fatalf("unexpected body: %s", response.Body.String())
			}
		})
	}
}

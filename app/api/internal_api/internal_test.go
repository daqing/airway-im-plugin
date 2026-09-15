package internal_api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daqing/airway-im-plugin/app/repo"
	"github.com/gin-gonic/gin"
)

const internalTestSecret = "internal-test-shared-secret"

func setupInternalTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	t.Setenv("IM_AUTH_SECRET", "internal-test-signing-secret")
	t.Setenv("IM_AUTH_SECRET_PREVIOUS", "")
	t.Setenv("IM_INTERNAL_SECRET", internalTestSecret)

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

	gin.SetMode(gin.TestMode)
	r := gin.New()
	Routes(r.Group("/"))
	return r
}

func issueCredential(t *testing.T, r *gin.Engine, body string) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/credentials", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-IM-Internal-Secret", internalTestSecret)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("issue status %d: %s", response.Code, response.Body.String())
	}
	var parsed struct {
		Data struct {
			Credential string `json:"credential"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &parsed); err != nil || parsed.Data.Credential == "" {
		t.Fatalf("invalid issue response: %s, %v", response.Body.String(), err)
	}
	return parsed.Data.Credential
}

func TestIssueCredentialThenAuthenticateRegistersUser(t *testing.T) {
	r := setupInternalTestRouter(t)
	credential := issueCredential(t, r, `{"uuid":"host-7","name":"alice","nickname":"Alice","ttl_seconds":3600}`)

	request := httptest.NewRequest(http.MethodGet, "/internal/v1/auth", nil)
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("X-IM-Internal-Secret", internalTestSecret)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("auth status %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Data struct {
			UserID int64 `json:"user_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Data.UserID != 1 {
		t.Fatalf("unexpected auth response: %s, %v", response.Body.String(), err)
	}
}

func TestIssueCredentialRejectsBadInputAndMissingSecret(t *testing.T) {
	r := setupInternalTestRouter(t)

	request := httptest.NewRequest(http.MethodPost, "/internal/v1/credentials", bytes.NewBufferString(`{"uuid":"","name":""}`))
	request.Header.Set("X-IM-Internal-Secret", internalTestSecret)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty identity, got %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/internal/v1/credentials", bytes.NewBufferString(`{"uuid":"u","name":"n"}`))
	response = httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without internal secret, got %d", response.Code)
	}
}

func TestAuthenticateRejectsCredentialWithoutInternalSecret(t *testing.T) {
	r := setupInternalTestRouter(t)
	credential := issueCredential(t, r, `{"uuid":"host-7","name":"alice"}`)

	request := httptest.NewRequest(http.MethodGet, "/internal/v1/auth", nil)
	request.Header.Set("Authorization", "Bearer "+credential)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without internal secret, got %d", response.Code)
	}
}

func TestIssuedCredentialBecomesInvalidAfterRevocation(t *testing.T) {
	r := setupInternalTestRouter(t)
	credential := issueCredential(t, r, `{"uuid":"host-9","name":"dave"}`)

	// The minted credential carries the user's current token_version.
	parts := strings.Split(credential, ".")
	if len(parts) != 3 {
		t.Fatalf("malformed credential: %q", credential)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims struct {
		TokenVersion int64 `json:"token_version"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.TokenVersion != 1 {
		t.Fatalf("token_version = %d, err = %v", claims.TokenVersion, err)
	}

	// Bumping the version (the admin revoke action) kills the credential.
	if _, err := repo.CurrentDB().Exec("UPDATE users SET token_version = token_version + 1 WHERE uuid = 'host-9'"); err != nil {
		t.Fatalf("bump token_version: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/internal/v1/auth", nil)
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("X-IM-Internal-Secret", internalTestSecret)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for revoked credential, got %d: %s", response.Code, response.Body.String())
	}
}

package admin_api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/repo"
	"github.com/gin-gonic/gin"
)

func setupAdminRouter(t *testing.T) *gin.Engine {
	t.Helper()
	db, err := repo.SetupDB("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE users (
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
    );
    CREATE TABLE outbox_events (
        id VARCHAR(26) UNIQUE,
        topic VARCHAR(64),
        aggregate_id VARCHAR(26),
        payload TEXT,
        created_at DATETIME NOT NULL,
        published_at DATETIME,
        attempts INTEGER NOT NULL DEFAULT 0
    );
    CREATE TABLE conversations (
        id VARCHAR(26) UNIQUE,
        kind VARCHAR(16) NOT NULL,
        title VARCHAR(255),
        avatar_url VARCHAR(2048),
        created_by BIGINT NOT NULL,
        next_sequence BIGINT NOT NULL DEFAULT 1,
        created_at DATETIME NOT NULL,
        updated_at DATETIME NOT NULL
    );
    CREATE TABLE conversation_members (
        conversation_id VARCHAR(26) NOT NULL,
        user_id BIGINT NOT NULL,
        role VARCHAR(16) NOT NULL,
        joined_at DATETIME NOT NULL,
        left_at DATETIME
    );
    CREATE TABLE messages (
        id VARCHAR(26) UNIQUE,
        conversation_id VARCHAR(26) NOT NULL,
        sender_id BIGINT NOT NULL,
        content TEXT NOT NULL,
        content_type VARCHAR(32) NOT NULL,
        sequence BIGINT NOT NULL,
        created_at DATETIME NOT NULL,
        is_illegal BOOLEAN NOT NULL DEFAULT 0,
        moderated_at DATETIME
    );`)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("IM_ADMIN_USERNAME", "admin")
	t.Setenv("IM_ADMIN_PASSWORD", "correct-horse-battery-staple")
	adminSessions.Lock()
	adminSessions.values = make(map[string]time.Time)
	adminSessions.Unlock()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	Routes(router.Group("/"))
	return router
}

func loginAdmin(t *testing.T, router *gin.Engine) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/admin/api/login", bytes.NewBufferString(`{"username":"admin","password":"correct-horse-battery-staple"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login status %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Data.Token == "" {
		t.Fatalf("invalid login response: %s, %v", response.Body.String(), err)
	}
	return body.Data.Token
}

func TestAdminLoginProtectsStatus(t *testing.T) {
	router := setupAdminRouter(t)
	request := httptest.NewRequest(http.MethodGet, "/admin/api/status", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
	if token := loginAdmin(t, router); len(token) != 64 {
		t.Fatalf("token length = %d", len(token))
	}
}

func TestAdminListsAllUsers(t *testing.T) {
	router := setupAdminRouter(t)
	_, err := repo.CurrentDB().Exec(`INSERT INTO users
		(uuid, username, nickname, email, created_at, updated_at)
		VALUES ('host-123', 'octocat', 'The Octocat', 'octocat@example.com', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}

	token := loginAdmin(t, router)
	request := httptest.NewRequest(http.MethodGet, "/admin/api/users", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", response.Code, response.Body.String())
	}
	for _, value := range []string{"host-123", "octocat", "The Octocat", "octocat@example.com"} {
		if !bytes.Contains(response.Body.Bytes(), []byte(value)) {
			t.Fatalf("list response missing %q: %s", value, response.Body.String())
		}
	}
}

func TestAdminRevokesUserCredentials(t *testing.T) {
	router := setupAdminRouter(t)
	db := repo.CurrentDB()
	if _, err := db.Exec(`INSERT INTO users
		(uuid, username, nickname, created_at, updated_at)
		VALUES ('host-9', 'dave', 'Dave', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	token := loginAdmin(t, router)

	// Revoking an unknown uuid is a 404.
	request := httptest.NewRequest(http.MethodPost, "/admin/api/users/nobody/revoke", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("revoke unknown user status %d: %s", response.Code, response.Body.String())
	}

	// Revoking bumps token_version; the gateway kick is skipped because
	// IM_GATEWAY_URL is unset in tests.
	request = httptest.NewRequest(http.MethodPost, "/admin/api/users/host-9/revoke", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("revoke status %d: %s", response.Code, response.Body.String())
	}
	for _, value := range []string{`"uuid":"host-9"`, `"token_version":2`, `"connections_kicked":0`} {
		if !bytes.Contains(response.Body.Bytes(), []byte(value)) {
			t.Fatalf("revoke response missing %q: %s", value, response.Body.String())
		}
	}
	var version int64
	if err := db.Get(&version, "SELECT token_version FROM users WHERE uuid = 'host-9'"); err != nil || version != 2 {
		t.Fatalf("stored token_version = %d, %v", version, err)
	}

	// The endpoint requires an admin session.
	request = httptest.NewRequest(http.MethodPost, "/admin/api/users/host-9/revoke", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("revoke without admin session status %d", response.Code)
	}
}

func TestAdminListsConversationsAndMessages(t *testing.T) {
	router := setupAdminRouter(t)
	db := repo.CurrentDB()
	now := time.Now().UTC().Truncate(time.Second)
	_, err := db.Exec(`INSERT INTO users
		(id, uuid, username, nickname, created_at, updated_at)
		VALUES (1, 'host-alice', 'alice', 'Alice', ?, ?),
		       (2, 'host-bob', 'bob', NULL, ?, ?);
		INSERT INTO conversations
		(id, kind, title, created_by, next_sequence, created_at, updated_at)
		VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 'group', 'Backend Team', 1, 2, ?, ?),
		       ('01J2Q7D4N5R8TK6VD3SZ1H0Y9N', 'direct', NULL, 1, 2, ?, ?);
		INSERT INTO conversation_members
		(conversation_id, user_id, role, joined_at)
		VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 1, 'owner', ?),
		       ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 2, 'member', ?),
		       ('01J2Q7D4N5R8TK6VD3SZ1H0Y9N', 1, 'member', ?),
		       ('01J2Q7D4N5R8TK6VD3SZ1H0Y9N', 2, 'member', ?);
		INSERT INTO messages
		(id, conversation_id, sender_id, content, content_type, sequence, created_at)
		VALUES ('01J2Q8A4FQ8NA8R6YDJ2M98K3Q', '01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 2, 'Hello team', 'text/plain', 1, ?),
		       ('01J2Q8A4FQ8NA8R6YDJ2M98K3R', '01J2Q7D4N5R8TK6VD3SZ1H0Y9N', 1, 'Hi bob', 'text/plain', 1, ?)`,
		now, now, now, now, now, now, now, now, now, now, now, now, now, now)
	if err != nil {
		t.Fatal(err)
	}

	token := loginAdmin(t, router)

	// Default listing covers both kinds; direct conversations carry their
	// participant pair.
	request := httptest.NewRequest(http.MethodGet, "/admin/api/conversations", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list conversations status %d: %s", response.Code, response.Body.String())
	}
	for _, value := range []string{
		"Backend Team",
		`"kind":"group"`,
		`"kind":"direct"`,
		`"member_count":2`,
		`"message_count":1`,
		`"username":"bob"`,
	} {
		if !bytes.Contains(response.Body.Bytes(), []byte(value)) {
			t.Fatalf("conversation response missing %q: %s", value, response.Body.String())
		}
	}

	// The kind filter narrows the listing to one kind.
	for _, scenario := range []struct {
		kind    string
		present string
		absent  string
	}{
		{kind: "group", present: `"kind":"group"`, absent: `"kind":"direct"`},
		{kind: "direct", present: `"kind":"direct"`, absent: `"kind":"group"`},
	} {
		request = httptest.NewRequest(http.MethodGet, "/admin/api/conversations?kind="+scenario.kind, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response = httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("list %s conversations status %d: %s", scenario.kind, response.Code, response.Body.String())
		}
		if !bytes.Contains(response.Body.Bytes(), []byte(scenario.present)) {
			t.Fatalf("%s conversation response missing %q: %s", scenario.kind, scenario.present, response.Body.String())
		}
		if bytes.Contains(response.Body.Bytes(), []byte(scenario.absent)) {
			t.Fatalf("%s conversation response includes %q: %s", scenario.kind, scenario.absent, response.Body.String())
		}
	}

	// Messages are listable for direct conversations too.
	request = httptest.NewRequest(http.MethodGet, "/admin/api/conversations/01J2Q7D4N5R8TK6VD3SZ1H0Y9N/messages", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list direct messages status %d: %s", response.Code, response.Body.String())
	}
	for _, value := range []string{"Hi bob", `"sender_username":"alice"`, `"sequence":1`} {
		if !bytes.Contains(response.Body.Bytes(), []byte(value)) {
			t.Fatalf("direct message response missing %q: %s", value, response.Body.String())
		}
	}

	request = httptest.NewRequest(http.MethodGet, "/admin/api/conversations/01J2Q7D4N5R8TK6VD3SZ1H0Y9M/messages", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list messages status %d: %s", response.Code, response.Body.String())
	}
	for _, value := range []string{"Hello team", `"sender_username":"bob"`, `"sequence":1`} {
		if !bytes.Contains(response.Body.Bytes(), []byte(value)) {
			t.Fatalf("message response missing %q: %s", value, response.Body.String())
		}
	}

	request = httptest.NewRequest(http.MethodPost, "/admin/api/messages/01J2Q8A4FQ8NA8R6YDJ2M98K3Q/mark-illegal", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("moderate message status %d: %s", response.Code, response.Body.String())
	}
	for _, value := range []string{"Hello team", `"is_illegal":true`} {
		if !bytes.Contains(response.Body.Bytes(), []byte(value)) {
			t.Fatalf("moderation response missing %q: %s", value, response.Body.String())
		}
	}
	var illegal bool
	if err := db.Get(&illegal, "SELECT is_illegal FROM messages WHERE id = '01J2Q8A4FQ8NA8R6YDJ2M98K3Q'"); err != nil || !illegal {
		t.Fatalf("message illegal state = %v, %v", illegal, err)
	}

	// Moderation covers direct conversation messages as well.
	request = httptest.NewRequest(http.MethodPost, "/admin/api/messages/01J2Q8A4FQ8NA8R6YDJ2M98K3R/mark-illegal", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("moderate direct message status %d: %s", response.Code, response.Body.String())
	}
	var directTargets int
	if err := db.Get(&directTargets, `SELECT COUNT(*) FROM outbox_events WHERE topic = 'message.moderated' AND aggregate_id = '01J2Q8A4FQ8NA8R6YDJ2M98K3R' AND payload LIKE '%user_uuids%'`); err != nil || directTargets != 1 {
		t.Fatalf("direct moderation event count = %d, %v", directTargets, err)
	}
	var moderationEvents int
	if err := db.Get(&moderationEvents, "SELECT COUNT(*) FROM outbox_events WHERE topic = 'message.moderated'"); err != nil || moderationEvents != 2 {
		t.Fatalf("moderation event count = %d, %v", moderationEvents, err)
	}
}

package im_api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daqing/airway-im-plugin/deps/im/app/auth"
	"github.com/daqing/airway-im-plugin/deps/im/app/repo"
	"github.com/gin-gonic/gin"
)

const groupTestSecret = "groups-test-signing-secret"

func groupOwnerCredential(t *testing.T) string {
	t.Helper()
	credential, err := auth.MintCredential(groupTestSecret, "uuid-owner", "owner", "", "", time.Hour)
	if err != nil {
		t.Fatalf("mint owner credential: %v", err)
	}
	return credential
}

func setupGroupTestRouter(t *testing.T) *gin.Engine {
	t.Helper()

	db, err := repo.SetupDB("sqlite://:memory:")
	if err != nil {
		t.Fatalf("setup database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	schema := []string{
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			uuid VARCHAR(64) NOT NULL UNIQUE,
			username VARCHAR(255) NOT NULL,
			nickname VARCHAR(255),
			avatar_url VARCHAR(2048),
			email VARCHAR(255),
			last_seen_at DATETIME,
			token_version BIGINT NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE conversations (
			id VARCHAR(26) NOT NULL UNIQUE,
			kind VARCHAR(16) NOT NULL,
			title VARCHAR(255),
			avatar_url VARCHAR(2048),
			created_by BIGINT NOT NULL,
			next_sequence BIGINT NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE conversation_members (
			conversation_id VARCHAR(26) NOT NULL,
			user_id BIGINT NOT NULL,
			role VARCHAR(16) NOT NULL DEFAULT 'member',
			joined_at DATETIME NOT NULL,
			left_at DATETIME,
			UNIQUE (conversation_id, user_id)
		)`,
		`CREATE TABLE direct_conversations (
			conversation_id VARCHAR(26) NOT NULL UNIQUE,
			user_id_low BIGINT NOT NULL,
			user_id_high BIGINT NOT NULL,
			UNIQUE (user_id_low, user_id_high)
		)`,
		`CREATE TABLE messages (
			id VARCHAR(26) NOT NULL UNIQUE,
			conversation_id VARCHAR(26) NOT NULL,
			sender_id BIGINT NOT NULL,
			content TEXT NOT NULL,
			content_type VARCHAR(32) NOT NULL,
			sequence BIGINT NOT NULL,
			created_at DATETIME NOT NULL,
			is_illegal BOOLEAN NOT NULL DEFAULT 0,
			moderated_at DATETIME,
			UNIQUE (conversation_id, sequence)
		)`,
		`CREATE TABLE message_idempotency (
			user_id BIGINT NOT NULL,
			idempotency_key VARCHAR(128) NOT NULL,
			request_hash VARCHAR(64) NOT NULL,
			message_id VARCHAR(26) NOT NULL,
			created_at DATETIME NOT NULL,
			UNIQUE (user_id, idempotency_key)
		)`,
		`CREATE TABLE outbox_events (
			id VARCHAR(26) NOT NULL UNIQUE,
			topic VARCHAR(64) NOT NULL,
			aggregate_id VARCHAR(26) NOT NULL,
			payload TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			published_at DATETIME,
			attempts INTEGER NOT NULL DEFAULT 0
		)`,
	}
	for _, statement := range schema {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("create test schema: %v", err)
		}
	}
	t.Setenv("IM_AUTH_SECRET", groupTestSecret)
	for id, username := range []string{"owner", "alice", "bob"} {
		if _, err := db.Exec(
			`INSERT INTO users (id, uuid, username) VALUES (?, ?, ?)`,
			id+1, fmt.Sprintf("uuid-%s", username), username,
		); err != nil {
			t.Fatalf("insert user: %v", err)
		}
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	Routes(router.Group("/api/v1"))
	return router
}

func TestCreateGroup(t *testing.T) {
	router := setupGroupTestRouter(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/group", strings.NewReader(
		`{"title":"Backend Team","member_ids":[2,3,2,1]}`,
	))
	request.Header.Set("Authorization", "Bearer "+groupOwnerCredential(t))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Code int                  `json:"code"`
		Data conversationResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 0 || body.Data.Kind != "group" || body.Data.Title == nil || *body.Data.Title != "Backend Team" || body.Data.CreatedBy != 1 {
		t.Fatalf("unexpected response: %#v", body)
	}
	if len(body.Data.ID) != 26 {
		t.Fatalf("group id = %q", body.Data.ID)
	}

	var members []struct {
		UserID int64  `db:"user_id"`
		Role   string `db:"role"`
	}
	db := repo.CurrentDB()
	if err := db.Select(&members, db.Rebind("SELECT user_id, role FROM conversation_members WHERE conversation_id = ? ORDER BY user_id"), body.Data.ID); err != nil {
		t.Fatalf("load members: %v", err)
	}
	if len(members) != 3 || members[0].UserID != 1 || members[0].Role != "owner" || members[1].Role != "member" || members[2].Role != "member" {
		t.Fatalf("unexpected members: %#v", members)
	}
}

func TestCreateGroupRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: `{"member_ids":`},
		{name: "unknown field", body: `{"member_ids":[2],"kind":"direct"}`},
		{name: "no other members", body: `{"member_ids":[1,0,-2]}`},
		{name: "unknown member", body: `{"member_ids":[999]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := setupGroupTestRouter(t)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/group", strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer "+groupOwnerCredential(t))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var count int
			if err := repo.CurrentDB().Get(&count, "SELECT COUNT(*) FROM conversations"); err != nil {
				t.Fatalf("count conversations: %v", err)
			}
			if count != 0 {
				t.Fatalf("created %d conversations", count)
			}
		})
	}
}

func TestCreateGroupRequiresAuthentication(t *testing.T) {
	router := setupGroupTestRouter(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/group", strings.NewReader(`{"member_ids":[2]}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":10001`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestListGroupConversations(t *testing.T) {
	router := setupGroupTestRouter(t)
	db := repo.CurrentDB()
	statements := []string{
		`INSERT INTO conversations (id, kind, title, created_by, next_sequence, created_at, updated_at)
		 VALUES ('01GROUP00000000000000000001', 'group', 'Older Group', 2, 1, '2026-07-20 00:00:00', '2026-07-21 00:00:00')`,
		`INSERT INTO conversations (id, kind, title, created_by, next_sequence, created_at, updated_at)
		 VALUES ('01GROUP00000000000000000002', 'group', 'Newest Group', 1, 1, '2026-07-22 00:00:00', '2026-07-23 00:00:00')`,
		`INSERT INTO conversations (id, kind, title, created_by, next_sequence, created_at, updated_at)
		 VALUES ('01DIRECT0000000000000000001', 'direct', NULL, 1, 1, '2026-07-23 00:00:00', '2026-07-24 00:00:00')`,
		`INSERT INTO conversations (id, kind, title, created_by, next_sequence, created_at, updated_at)
		 VALUES ('01LEFT000000000000000000001', 'group', 'Left Group', 2, 1, '2026-07-24 00:00:00', '2026-07-25 00:00:00')`,
		`INSERT INTO conversations (id, kind, title, created_by, next_sequence, created_at, updated_at)
		 VALUES ('01OTHER00000000000000000001', 'group', 'Other Group', 2, 1, '2026-07-25 00:00:00', '2026-07-26 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('01GROUP00000000000000000001', 1, 'member', '2026-07-20 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('01GROUP00000000000000000002', 1, 'owner', '2026-07-22 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('01DIRECT0000000000000000001', 1, 'member', '2026-07-23 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at, left_at)
		 VALUES ('01LEFT000000000000000000001', 1, 'member', '2026-07-24 00:00:00', '2026-07-25 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('01OTHER00000000000000000001', 2, 'owner', '2026-07-25 00:00:00')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed conversations: %v", err)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/conversations?type=group", nil)
	request.Header.Set("Authorization", "Bearer "+groupOwnerCredential(t))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Code int                    `json:"code"`
		Data []conversationResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 0 || len(body.Data) != 2 {
		t.Fatalf("unexpected response: %#v", body)
	}
	if body.Data[0].Title == nil || *body.Data[0].Title != "Newest Group" ||
		body.Data[1].Title == nil || *body.Data[1].Title != "Older Group" {
		t.Fatalf("unexpected order or groups: %#v", body.Data)
	}
}

func TestListGroupConversationsReturnsEmptyArray(t *testing.T) {
	router := setupGroupTestRouter(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/conversations?type=group", nil)
	request.Header.Set("Authorization", "Bearer "+groupOwnerCredential(t))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"data":[]`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestListGroupConversationsRejectsInvalidType(t *testing.T) {
	tests := []string{
		"/api/v1/conversations",
		"/api/v1/conversations?type=direct",
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			router := setupGroupTestRouter(t)
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("Authorization", "Bearer "+groupOwnerCredential(t))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":10003`) {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestGetConversationReturnsTypeAndActiveMembers(t *testing.T) {
	router := setupGroupTestRouter(t)
	db := repo.CurrentDB()
	conversationUUID := "01J2Q7D4N5R8TK6VD3SZ1H0Y9M"
	statements := []string{
		`INSERT INTO conversations (id, kind, title, created_by, next_sequence, created_at, updated_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 'group', 'Backend Team', 1, 1, '2026-07-24 00:00:00', '2026-07-24 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 1, 'owner', '2026-07-24 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 2, 'member', '2026-07-24 00:01:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at, left_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 3, 'member', '2026-07-24 00:02:00', '2026-07-24 00:03:00')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed conversation: %v", err)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/"+conversationUUID, nil)
	request.Header.Set("Authorization", "Bearer "+groupOwnerCredential(t))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Code int                         `json:"code"`
		Data conversationDetailsResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 0 || body.Data.ConversationUUID != conversationUUID || body.Data.Type != "group" {
		t.Fatalf("unexpected conversation: %#v", body)
	}
	if len(body.Data.Members) != 2 ||
		body.Data.Members[0].ID != 1 || body.Data.Members[0].Role != "owner" ||
		body.Data.Members[1].ID != 2 || body.Data.Members[1].Role != "member" {
		t.Fatalf("unexpected members: %#v", body.Data.Members)
	}
}

func TestGetConversationReturnsDirectType(t *testing.T) {
	router := setupGroupTestRouter(t)
	db := repo.CurrentDB()
	conversationUUID := "01J2Q7D4N5R8TK6VD3SZ1H0Y8M"
	statements := []string{
		`INSERT INTO conversations (id, kind, created_by, next_sequence, created_at, updated_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y8M', 'direct', 1, 1, '2026-07-24 00:00:00', '2026-07-24 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y8M', 1, 'member', '2026-07-24 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y8M', 2, 'member', '2026-07-24 00:00:00')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed direct conversation: %v", err)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/"+conversationUUID, nil)
	request.Header.Set("Authorization", "Bearer "+groupOwnerCredential(t))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"type":"direct"`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestGetConversationRejectsUnavailableConversation(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "invalid UUID", path: "/api/v1/conversations/invalid"},
		{name: "unknown conversation", path: "/api/v1/conversations/01J2Q7D4N5R8TK6VD3SZ1H0Y7M"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := setupGroupTestRouter(t)
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Authorization", "Bearer "+groupOwnerCredential(t))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest && response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestCreateConversationMessage(t *testing.T) {
	router := setupGroupTestRouter(t)
	db := repo.CurrentDB()
	conversationUUID := "01J2Q7D4N5R8TK6VD3SZ1H0Y9M"
	statements := []string{
		`INSERT INTO conversations (id, kind, title, created_by, next_sequence, created_at, updated_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 'group', 'Backend Team', 1, 1, '2026-07-24 00:00:00', '2026-07-24 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 1, 'owner', '2026-07-24 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 2, 'member', '2026-07-24 00:00:00')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed conversation: %v", err)
		}
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/conversations/"+conversationUUID+"/messages",
		strings.NewReader(`{"content":"Hello group","content_type":"text/plain"}`),
	)
	request.Header.Set("Authorization", "Bearer "+groupOwnerCredential(t))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Code int             `json:"code"`
		Data messageResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 0 || body.Data.ConversationID != conversationUUID ||
		body.Data.Content != "Hello group" || body.Data.ContentType != "text/plain" ||
		body.Data.Sequence != 1 || body.Data.Sender.ID != 1 {
		t.Fatalf("unexpected message: %#v", body)
	}

	var messageCount, outboxCount int
	if err := db.Get(&messageCount, "SELECT COUNT(*) FROM messages"); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if err := db.Get(&outboxCount, "SELECT COUNT(*) FROM outbox_events WHERE topic = 'message.created'"); err != nil {
		t.Fatalf("count outbox events: %v", err)
	}
	if messageCount != 1 || outboxCount != 1 {
		t.Fatalf("message count = %d, outbox count = %d", messageCount, outboxCount)
	}
}

func TestCreateConversationMessageRejectsNonMember(t *testing.T) {
	router := setupGroupTestRouter(t)
	db := repo.CurrentDB()
	conversationUUID := "01J2Q7D4N5R8TK6VD3SZ1H0Y9M"
	if _, err := db.Exec(
		`INSERT INTO conversations (id, kind, title, created_by, next_sequence, created_at, updated_at)
		 VALUES (?, 'group', 'Other Group', 2, 1, '2026-07-24 00:00:00', '2026-07-24 00:00:00')`,
		conversationUUID,
	); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES (?, 2, 'owner', '2026-07-24 00:00:00')`,
		conversationUUID,
	); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/conversations/"+conversationUUID+"/messages",
		strings.NewReader(`{"content":"Not allowed"}`),
	)
	request.Header.Set("Authorization", "Bearer "+groupOwnerCredential(t))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":10005`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestListMessagesAfterSequenceIncludesSenderAndContent(t *testing.T) {
	router := setupGroupTestRouter(t)
	db := repo.CurrentDB()
	conversationUUID := "01J2Q7D4N5R8TK6VD3SZ1H0Y9M"
	statements := []string{
		`INSERT INTO conversations (id, kind, title, created_by, next_sequence, created_at, updated_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 'group', 'Backend Team', 1, 4, '2026-07-24 00:00:00', '2026-07-24 00:03:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 1, 'owner', '2026-07-24 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 2, 'member', '2026-07-24 00:00:00')`,
		`INSERT INTO messages (id, conversation_id, sender_id, content, content_type, sequence, created_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0A1M', '01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 1, 'First', 'text/plain', 1, '2026-07-24 00:01:00')`,
		`INSERT INTO messages (id, conversation_id, sender_id, content, content_type, sequence, created_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0A2M', '01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 2, 'Second', 'text/markdown', 2, '2026-07-24 00:02:00')`,
		`INSERT INTO messages (id, conversation_id, sender_id, content, content_type, sequence, created_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0A3M', '01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 1, 'Third', 'text/plain', 3, '2026-07-24 00:03:00')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed messages: %v", err)
		}
	}

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/conversations/"+conversationUUID+"/messages?after_sequence=1",
		nil,
	)
	request.Header.Set("Authorization", "Bearer "+groupOwnerCredential(t))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Code int               `json:"code"`
		Data []messageResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 0 || len(body.Data) != 2 {
		t.Fatalf("unexpected response: %#v", body)
	}
	if body.Data[0].Sequence != 2 || body.Data[0].Content != "Second" ||
		body.Data[0].Sender.ID != 2 || body.Data[0].Sender.Username != "alice" ||
		body.Data[1].Sequence != 3 || body.Data[1].Content != "Third" {
		t.Fatalf("unexpected messages: %#v", body.Data)
	}
}

func TestListMessagesMasksIllegalContent(t *testing.T) {
	router := setupGroupTestRouter(t)
	db := repo.CurrentDB()
	conversationID := "01J2Q7D4N5R8TK6VD3SZ1H0Y9M"
	statements := []string{
		`INSERT INTO conversations (id, kind, title, created_by, next_sequence, created_at, updated_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 'group', 'Backend Team', 1, 2, '2026-07-24 00:00:00', '2026-07-24 00:01:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 1, 'owner', '2026-07-24 00:00:00')`,
		`INSERT INTO messages
			(id, conversation_id, sender_id, content, content_type, sequence, created_at, is_illegal)
		 VALUES ('01J2Q8A4FQ8NA8R6YDJ2M98K3Z', '01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 2, 'prohibited content', 'text/plain', 1, '2026-07-24 00:01:00', 1)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed illegal message: %v", err)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/"+conversationID+"/messages?after_sequence=0", nil)
	request.Header.Set("Authorization", "Bearer "+groupOwnerCredential(t))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"content":"***"`) {
		t.Fatalf("illegal message was not masked: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "prohibited content") {
		t.Fatalf("illegal content leaked: %s", response.Body.String())
	}
}

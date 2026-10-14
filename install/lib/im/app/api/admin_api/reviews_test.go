package admin_api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/repo"
	"github.com/gin-gonic/gin"
)

const (
	reviewConversationID = "01J2Q7D4N5R8TK6VD3SZ1H0Y9M"
	pendingReviewID      = "01J2Q8A4FQ8NA8R6YDJ2M98K3Q"
	decidedReviewID      = "01J2Q8A4FQ8NA8R6YDJ2M98K3R"
)

// seedReviews installs a group conversation whose member bob has one queued
// message waiting for an operator, plus one entry an operator already
// discarded.
func seedReviews(t *testing.T) {
	t.Helper()
	db := repo.CurrentDB()
	now := time.Now().UTC().Truncate(time.Second)
	statements := []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO users (id, uuid, username, nickname, created_at, updated_at)
				VALUES (1, 'host-alice', 'alice', 'Alice', ?, ?), (2, 'host-bob', 'bob', 'Bob', ?, ?)`,
			args: []any{now, now, now, now},
		},
		{
			query: `INSERT INTO conversations (id, kind, title, created_by, next_sequence, created_at, updated_at)
				VALUES (?, 'group', 'Backend Team', 1, 1, ?, ?)`,
			args: []any{reviewConversationID, now, now},
		},
		{
			query: `INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
				VALUES (?, 1, 'owner', ?), (?, 2, 'member', ?)`,
			args: []any{reviewConversationID, now, reviewConversationID, now},
		},
		{
			query: `INSERT INTO message_reviews
				(id, conversation_id, sender_id, content, content_type, flag_labels, flag_matches, status, created_at)
				VALUES (?, ?, 2, 'Hello team', 'text/plain', '["illegal"]', '["网络赌博"]', 'pending', ?)`,
			args: []any{pendingReviewID, reviewConversationID, now},
		},
		{
			query: `INSERT INTO message_reviews
				(id, conversation_id, sender_id, content, content_type, flag_labels, status, created_at)
				VALUES (?, ?, 2, 'Old message', 'text/plain', '[]', 'rejected', ?)`,
			args: []any{decidedReviewID, reviewConversationID, now},
		},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed reviews: %v", err)
		}
	}
}

func adminRequest(t *testing.T, router *gin.Engine, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestListReviewsDefaultsToPending(t *testing.T) {
	router := setupAdminRouter(t)
	seedReviews(t)
	token := loginAdmin(t, router)

	response := adminRequest(t, router, http.MethodGet, "/admin/api/reviews", token)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	for _, value := range []string{
		pendingReviewID,
		`"sender_username":"bob"`,
		`"conversation_title":"Backend Team"`,
		`"flag_labels":["illegal"]`,
		`"flag_matches":["网络赌博"]`,
		`"status":"pending"`,
	} {
		if !bytes.Contains(response.Body.Bytes(), []byte(value)) {
			t.Fatalf("review response missing %q: %s", value, response.Body.String())
		}
	}
	if bytes.Contains(response.Body.Bytes(), []byte(decidedReviewID)) {
		t.Fatalf("pending listing includes a decided review: %s", response.Body.String())
	}

	response = adminRequest(t, router, http.MethodGet, "/admin/api/reviews?status=rejected", token)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(decidedReviewID)) {
		t.Fatalf("rejected listing: %d, %s", response.Code, response.Body.String())
	}

	response = adminRequest(t, router, http.MethodGet, "/admin/api/reviews?status=bogus", token)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid status filter: %d, %s", response.Code, response.Body.String())
	}
}

func TestApproveReviewDeliversMessage(t *testing.T) {
	router := setupAdminRouter(t)
	seedReviews(t)
	token := loginAdmin(t, router)
	db := repo.CurrentDB()

	response := adminRequest(t, router, http.MethodPost, "/admin/api/reviews/"+pendingReviewID+"/approve", token)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	for _, value := range []string{`"status":"approved"`, `"reviewed_by":"admin"`, `"message_id":"` + pendingReviewID + `"`} {
		if !bytes.Contains(response.Body.Bytes(), []byte(value)) {
			t.Fatalf("approve response missing %q: %s", value, response.Body.String())
		}
	}

	// The message is delivered under the review record's own id, so the
	// sender's pending bubble becomes it instead of a duplicate.
	var sequence int64
	if err := db.Get(&sequence, "SELECT sequence FROM messages WHERE id = ?", pendingReviewID); err != nil || sequence != 1 {
		t.Fatalf("delivered sequence = %d, %v", sequence, err)
	}
	var nextSequence int64
	if err := db.Get(&nextSequence, "SELECT next_sequence FROM conversations WHERE id = ?", reviewConversationID); err != nil || nextSequence != 2 {
		t.Fatalf("next_sequence = %d, %v", nextSequence, err)
	}
	var eventCount int
	if err := db.Get(&eventCount, "SELECT COUNT(*) FROM outbox_events WHERE topic = 'message.created' AND aggregate_id = ?", pendingReviewID); err != nil || eventCount != 1 {
		t.Fatalf("created event count = %d, %v", eventCount, err)
	}
}

func TestApproveReviewRejectsDecidedAndUnknownEntries(t *testing.T) {
	router := setupAdminRouter(t)
	seedReviews(t)
	token := loginAdmin(t, router)

	response := adminRequest(t, router, http.MethodPost, "/admin/api/reviews/"+decidedReviewID+"/approve", token)
	if response.Code != http.StatusConflict {
		t.Fatalf("already decided: %d, %s", response.Code, response.Body.String())
	}
	response = adminRequest(t, router, http.MethodPost, "/admin/api/reviews/01J2Q8A4FQ8NA8R6YDJ2M98K3Z/approve", token)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown review: %d, %s", response.Code, response.Body.String())
	}
}

func TestRejectReviewLabelsSenderAndNotifiesThem(t *testing.T) {
	router := setupAdminRouter(t)
	seedReviews(t)
	token := loginAdmin(t, router)
	db := repo.CurrentDB()

	response := adminRequest(t, router, http.MethodPost, "/admin/api/reviews/"+pendingReviewID+"/reject", token)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	for _, value := range []string{`"status":"rejected"`, `"reviewed_by":"admin"`} {
		if !bytes.Contains(response.Body.Bytes(), []byte(value)) {
			t.Fatalf("reject response missing %q: %s", value, response.Body.String())
		}
	}

	// The message is discarded, never delivered.
	if count := countAdminRows(t, "SELECT COUNT(*) FROM messages"); count != 0 {
		t.Fatalf("messages = %d", count)
	}
	if count := countAdminRows(t, "SELECT COUNT(*) FROM message_reviews WHERE id = ? AND message_id IS NULL", pendingReviewID); count != 1 {
		t.Fatalf("rejected review without message id = %d", count)
	}

	// The sender carries the label, so their later messages are queued too.
	if count := countAdminRows(t, `SELECT COUNT(*) FROM users WHERE id = 2 AND labels = '["illegal"]'`); count != 1 {
		t.Fatalf("sender labels = %d", count)
	}
	// Other members are untouched.
	if count := countAdminRows(t, `SELECT COUNT(*) FROM users WHERE id = 1 AND labels = '[]'`); count != 1 {
		t.Fatalf("other user labels = %d", count)
	}

	var payload string
	if err := db.Get(&payload, "SELECT payload FROM outbox_events WHERE topic = 'message.review_rejected' AND aggregate_id = ?", pendingReviewID); err != nil {
		t.Fatalf("rejection event: %v", err)
	}
	for _, value := range []string{`"message_id":"` + pendingReviewID + `"`, `"conversation_id":"` + reviewConversationID + `"`, `"user_uuids":["host-bob"]`} {
		if !bytes.Contains([]byte(payload), []byte(value)) {
			t.Fatalf("rejection payload missing %q: %s", value, payload)
		}
	}
	if count := countAdminRows(t, "SELECT COUNT(*) FROM outbox_events WHERE topic = 'message.created'"); count != 0 {
		t.Fatalf("created events = %d", count)
	}
}

func countAdminRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var count int
	if err := repo.CurrentDB().Get(&count, query, args...); err != nil {
		t.Fatalf("count %s: %v", query, err)
	}
	return count
}

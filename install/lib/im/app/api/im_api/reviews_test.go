package im_api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/moderation"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/repo"
	"github.com/gin-gonic/gin"
)

const reviewConversationUUID = "01J2Q7D4N5R8TK6VD3SZ1H0Y9M"

func seedReviewConversation(t *testing.T) {
	t.Helper()
	db := repo.CurrentDB()
	statements := []string{
		`INSERT INTO conversations (id, kind, title, created_by, next_sequence, created_at, updated_at)
		 VALUES ('` + reviewConversationUUID + `', 'group', 'Backend Team', 1, 1, '2026-07-24 00:00:00', '2026-07-24 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('` + reviewConversationUUID + `', 1, 'owner', '2026-07-24 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('` + reviewConversationUUID + `', 2, 'member', '2026-07-24 00:00:00')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed conversation: %v", err)
		}
	}
}

// postMessage sends through the single-message endpoint; an empty key omits
// the Idempotency-Key header.
func postMessage(t *testing.T, router *gin.Engine, body, key string) (int, messageResponse, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/messages", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+groupOwnerCredential(t))
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	var envelope struct {
		Code int             `json:"code"`
		Data messageResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response %s: %v", response.Body.String(), err)
	}
	return response.Code, envelope.Data, response.Body.String()
}

func countRows(t *testing.T, query string) int {
	t.Helper()
	var count int
	if err := repo.CurrentDB().Get(&count, query); err != nil {
		t.Fatalf("count %s: %v", query, err)
	}
	return count
}

func TestCreateMessageQueuesClientFlaggedMessage(t *testing.T) {
	router := setupGroupTestRouter(t)
	seedReviewConversation(t)

	status, message, body := postMessage(t, router,
		`{"conversation_id":"`+reviewConversationUUID+`","content":"hello","content_type":"text/plain","labels":["illegal"]}`, "")

	if status != http.StatusCreated || message.ReviewStatus != moderation.ReviewPending {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	if message.Sequence != 0 || message.ID == "" || message.ConversationID != reviewConversationUUID {
		t.Fatalf("unexpected queued message: %#v", message)
	}
	// The message is parked, not delivered: no member has it, and nothing is
	// fanned out.
	if got := countRows(t, "SELECT COUNT(*) FROM messages"); got != 0 {
		t.Fatalf("messages = %d", got)
	}
	if got := countRows(t, "SELECT COUNT(*) FROM message_reviews WHERE status = 'pending'"); got != 1 {
		t.Fatalf("pending reviews = %d", got)
	}
	if got := countRows(t, "SELECT COUNT(*) FROM outbox_events"); got != 0 {
		t.Fatalf("outbox events = %d", got)
	}

	var flagLabels string
	if err := repo.CurrentDB().Get(&flagLabels, "SELECT flag_labels FROM message_reviews"); err != nil {
		t.Fatalf("read flag labels: %v", err)
	}
	if flagLabels != `["illegal"]` {
		t.Fatalf("flag labels = %s", flagLabels)
	}
}

func TestCreateMessageQueuesScreenedMessage(t *testing.T) {
	router := setupGroupTestRouter(t)
	seedReviewConversation(t)
	screenWith(t, moderation.ScreenResult{Flagged: true, Categories: []string{"illegal"}, Words: []string{"网络赌博"}}, nil)

	status, message, body := postMessage(t, router,
		`{"conversation_id":"`+reviewConversationUUID+`","content":"要不要一起网络赌博"}`, "")

	if status != http.StatusCreated || message.ReviewStatus != moderation.ReviewPending {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	// The screening service's finding is recorded, so an operator sees why
	// the message was queued.
	var flagLabels, flagMatches string
	if err := repo.CurrentDB().Get(&flagLabels, "SELECT flag_labels FROM message_reviews"); err != nil {
		t.Fatalf("read flag labels: %v", err)
	}
	if err := repo.CurrentDB().Get(&flagMatches, "SELECT flag_matches FROM message_reviews"); err != nil {
		t.Fatalf("read flag matches: %v", err)
	}
	if flagLabels != `["illegal"]` {
		t.Fatalf("flag labels = %s", flagLabels)
	}
	if flagMatches != `["网络赌博"]` {
		t.Fatalf("flag matches = %s", flagMatches)
	}
}

// Screening that cannot run is not a pass: the message is held, and the queue
// records that nobody looked at it.
func TestCreateMessageQueuesUnavailableScreening(t *testing.T) {
	router := setupGroupTestRouter(t)
	seedReviewConversation(t)
	screenWith(t, moderation.ScreenResult{}, errors.New("moderation: screen: connection refused"))

	status, message, body := postMessage(t, router,
		`{"conversation_id":"`+reviewConversationUUID+`","content":"新版本什么时候发？"}`, "")

	if status != http.StatusCreated || message.ReviewStatus != moderation.ReviewPending || message.Sequence != 0 {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	if got := countRows(t, "SELECT COUNT(*) FROM messages"); got != 0 {
		t.Fatalf("messages = %d", got)
	}
	var flagMatches string
	if err := repo.CurrentDB().Get(&flagMatches, "SELECT flag_matches FROM message_reviews"); err != nil {
		t.Fatalf("read flag matches: %v", err)
	}
	if flagMatches != `["screening-unavailable"]` {
		t.Fatalf("flag matches = %s", flagMatches)
	}
}

func TestCreateMessageQueuesEveryMessageFromLabeledSender(t *testing.T) {
	router := setupGroupTestRouter(t)
	seedReviewConversation(t)
	if _, err := repo.CurrentDB().Exec(`UPDATE users SET labels = '["illegal"]' WHERE id = 1`); err != nil {
		t.Fatalf("label sender: %v", err)
	}

	status, message, body := postMessage(t, router,
		`{"conversation_id":"`+reviewConversationUUID+`","content":"新版本什么时候发？"}`, "")

	if status != http.StatusCreated || message.ReviewStatus != moderation.ReviewPending {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	if got := countRows(t, "SELECT COUNT(*) FROM messages"); got != 0 {
		t.Fatalf("messages = %d", got)
	}
}

func TestCreateMessageDeliversUnflaggedMessage(t *testing.T) {
	router := setupGroupTestRouter(t)
	seedReviewConversation(t)

	status, message, _ := postMessage(t, router,
		`{"conversation_id":"`+reviewConversationUUID+`","content":"新版本什么时候发？"}`, "")

	if status != http.StatusCreated || message.ReviewStatus != "" || message.Sequence != 1 {
		t.Fatalf("status = %d, message = %#v", status, message)
	}
	if got := countRows(t, "SELECT COUNT(*) FROM message_reviews"); got != 0 {
		t.Fatalf("reviews = %d", got)
	}
	if got := countRows(t, "SELECT COUNT(*) FROM outbox_events WHERE topic = 'message.created'"); got != 1 {
		t.Fatalf("created events = %d", got)
	}
}

func TestCreateMessageReplaysQueuedMessageOnIdempotentRetry(t *testing.T) {
	router := setupGroupTestRouter(t)
	seedReviewConversation(t)
	body := `{"conversation_id":"` + reviewConversationUUID + `","content":"买点冰毒","labels":["illegal"]}`

	status, first, _ := postMessage(t, router, body, "retry-key")
	if status != http.StatusCreated || first.ReviewStatus != moderation.ReviewPending {
		t.Fatalf("first send: status = %d, message = %#v", status, first)
	}
	status, second, responseBody := postMessage(t, router, body, "retry-key")
	if status != http.StatusOK || second.ID != first.ID || second.ReviewStatus != moderation.ReviewPending {
		t.Fatalf("retry: status = %d, body = %s", status, responseBody)
	}
	if got := countRows(t, "SELECT COUNT(*) FROM message_reviews"); got != 1 {
		t.Fatalf("reviews = %d", got)
	}
}

func TestCreateMessageRejectsOversizedLabelSet(t *testing.T) {
	router := setupGroupTestRouter(t)
	seedReviewConversation(t)

	labels := make([]string, maxLabels+1)
	for i := range labels {
		labels[i] = `"illegal"`
	}
	status, _, body := postMessage(t, router,
		`{"conversation_id":"`+reviewConversationUUID+`","content":"hello","labels":[`+strings.Join(labels, ",")+`]}`, "")

	if status != http.StatusBadRequest || !strings.Contains(body, `"code":10003`) {
		t.Fatalf("status = %d, body = %s", status, body)
	}
}

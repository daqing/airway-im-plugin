package internal_api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/daqing/airway/lib/repo"
	"github.com/gin-gonic/gin"
)

func setupNotifyOutbox(t *testing.T) {
	t.Helper()
	if _, err := repo.CurrentDB().Conn().Exec(`CREATE TABLE IF NOT EXISTS outbox_events (
		id VARCHAR(26) NOT NULL UNIQUE,
		topic VARCHAR(64) NOT NULL,
		aggregate_id VARCHAR(26) NOT NULL,
		payload TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		published_at DATETIME,
		attempts INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatalf("create outbox_events: %v", err)
	}
}

func postNotify(t *testing.T, router *gin.Engine, secret, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/notify", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	if secret != "" {
		request.Header.Set("X-IM-Internal-Secret", secret)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestNotifyEnqueuesFanoutFrame(t *testing.T) {
	router := setupInternalTestRouter(t)
	setupNotifyOutbox(t)

	response := postNotify(t, router, internalTestSecret,
		`{"user_uuids":["user-2","user-2",""],"event":"host.friend_request","data":{"from_uuid":"user-1"}}`)
	if response.Code != http.StatusOK {
		t.Fatalf("notify status %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Data struct {
			EventID string `json:"event_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Data.EventID == "" {
		t.Fatalf("unexpected notify response: %s, %v", response.Body.String(), err)
	}

	var topic, payload string
	if err := repo.CurrentDB().Conn().
		QueryRow(`SELECT topic, payload FROM outbox_events WHERE id = ?`, body.Data.EventID).
		Scan(&topic, &payload); err != nil {
		t.Fatalf("load queued event: %v", err)
	}
	if topic != "host.friend_request" {
		t.Fatalf("topic = %q", topic)
	}
	var frame struct {
		Event     string   `json:"event"`
		EventID   string   `json:"event_id"`
		Data      struct {
			FromUUID string `json:"from_uuid"`
		} `json:"data"`
		Targets struct {
			UserUUIDs []string `json:"user_uuids"`
		} `json:"targets"`
	}
	if err := json.Unmarshal([]byte(payload), &frame); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if frame.Event != "host.friend_request" || frame.EventID != body.Data.EventID {
		t.Fatalf("unexpected frame header: %+v", frame)
	}
	if frame.Data.FromUUID != "user-1" {
		t.Fatalf("data.from_uuid = %q", frame.Data.FromUUID)
	}
	// Duplicates and blanks are dropped, so the deduped recipient list has
	// exactly one entry.
	if len(frame.Targets.UserUUIDs) != 1 || frame.Targets.UserUUIDs[0] != "user-2" {
		t.Fatalf("targets = %v", frame.Targets.UserUUIDs)
	}
}

func TestNotifyRejectsBadInputAndMissingSecret(t *testing.T) {
	router := setupInternalTestRouter(t)
	setupNotifyOutbox(t)

	if response := postNotify(t, router, "", `{"user_uuids":["user-2"],"event":"x"}`); response.Code != http.StatusUnauthorized {
		t.Fatalf("missing secret status %d", response.Code)
	}
	if response := postNotify(t, router, internalTestSecret, `{"user_uuids":[],"event":"x"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("empty recipients status %d", response.Code)
	}
	if response := postNotify(t, router, internalTestSecret, `{"user_uuids":["user-2"],"event":""}`); response.Code != http.StatusBadRequest {
		t.Fatalf("empty event status %d", response.Code)
	}
}

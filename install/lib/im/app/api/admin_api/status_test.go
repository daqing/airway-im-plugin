package admin_api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/repo"
)

func TestStatusWithPendingOutboxEvents(t *testing.T) {
	router := setupAdminRouter(t)
	token := loginAdmin(t, router)

	db := repo.CurrentDB()
	insertPending := db.Rebind(`INSERT INTO outbox_events (id, topic, aggregate_id, payload, created_at, attempts)
		VALUES (?, 'message.created', '01AGG', '{}', ?, 2)`)
	if _, err := db.Exec(insertPending, "01PENDINGTEST", time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/api/status with pending outbox events: got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			OutboxPublisher struct {
				Pending          int64   `json:"pending"`
				OldestPendingSec float64 `json:"oldest_pending_seconds"`
			} `json:"outbox_publisher"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid status response: %s, %v", rec.Body.String(), err)
	}
	if body.Data.OutboxPublisher.Pending != 1 {
		t.Fatalf("status pending: got %d, want 1", body.Data.OutboxPublisher.Pending)
	}
	if body.Data.OutboxPublisher.OldestPendingSec <= 0 {
		t.Fatalf("status oldest_pending_seconds: got %f, want > 0", body.Data.OutboxPublisher.OldestPendingSec)
	}
}

func TestStatusWithEmptyOutbox(t *testing.T) {
	router := setupAdminRouter(t)
	token := loginAdmin(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/api/status with empty outbox: got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

package admin_api

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/auth"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/repo"
)

func TestBanReviewSenderLabelsRevokesAndDiscards(t *testing.T) {
	t.Setenv("IM_AUTH_SECRET", "ban-test-signing-secret")
	router := setupAdminRouter(t)
	seedReviews(t)
	token := loginAdmin(t, router)
	db := repo.CurrentDB()

	response := adminRequest(t, router, http.MethodPost, "/admin/api/reviews/"+pendingReviewID+"/ban", token)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	// The gateway is unreachable in tests, so the kick is a no-op (0).
	for _, value := range []string{`"banned_user_uuid":"host-bob"`, `"connections_kicked":0`, `"status":"rejected"`, `"reviewed_by":"admin"`} {
		if !bytes.Contains(response.Body.Bytes(), []byte(value)) {
			t.Fatalf("ban response missing %q: %s", value, response.Body.String())
		}
	}

	// The message is discarded, never delivered.
	if count := countAdminRows(t, "SELECT COUNT(*) FROM messages"); count != 0 {
		t.Fatalf("messages = %d", count)
	}
	// The sender is banned; other users are untouched.
	if count := countAdminRows(t, `SELECT COUNT(*) FROM users WHERE id = 2 AND labels = '["banned"]'`); count != 1 {
		t.Fatalf("sender labels = %d", count)
	}
	if count := countAdminRows(t, `SELECT COUNT(*) FROM users WHERE id = 1 AND labels = '[]'`); count != 1 {
		t.Fatalf("other user labels = %d", count)
	}
	// Credentials are revoked.
	var version int64
	if err := db.Get(&version, "SELECT token_version FROM users WHERE id = 2"); err != nil || version != 2 {
		t.Fatalf("token_version = %d, %v", version, err)
	}

	// The banned account no longer authenticates — even with an unversioned
	// credential that skips the revocation check.
	credential, err := auth.MintCredential("ban-test-signing-secret", "host-bob", "bob", "", "", time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if user, err := auth.UserFromHeader("Bearer " + credential); err != nil || user != nil {
		t.Fatalf("banned user authenticated: %#v, %v", user, err)
	}
}

func TestBanReviewSenderAllowsDecidedEntries(t *testing.T) {
	router := setupAdminRouter(t)
	seedReviews(t)
	token := loginAdmin(t, router)

	response := adminRequest(t, router, http.MethodPost, "/admin/api/reviews/"+decidedReviewID+"/ban", token)
	if response.Code != http.StatusOK {
		t.Fatalf("ban from a decided entry: %d, %s", response.Code, response.Body.String())
	}
	if count := countAdminRows(t, `SELECT COUNT(*) FROM users WHERE id = 2 AND labels = '["banned"]'`); count != 1 {
		t.Fatalf("sender labels = %d", count)
	}
}

func TestBanReviewSenderRejectsUnknownReviews(t *testing.T) {
	router := setupAdminRouter(t)
	seedReviews(t)
	token := loginAdmin(t, router)

	response := adminRequest(t, router, http.MethodPost, "/admin/api/reviews/01J2Q8A4FQ8NA8R6YDJ2M98K3Z/ban", token)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown review: %d, %s", response.Code, response.Body.String())
	}
	if count := countAdminRows(t, `SELECT COUNT(*) FROM users WHERE labels != '[]'`); count != 0 {
		t.Fatalf("a user was labeled on the failed ban: %d", count)
	}
}

package implugin

import (
	"strings"
	"testing"
	"time"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/auth"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/repo"
)

func setupIssueTestDB(t *testing.T) {
	t.Helper()
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
}

func TestIssueCredentialMintsRevocableCredentialInProcess(t *testing.T) {
	t.Setenv("IM_AUTH_SECRET", "test-auth-secret")
	setupIssueTestDB(t)

	credential, expiresAt, err := IssueCredential("host-7", "alice", "Alice", "", time.Hour)
	if err != nil {
		t.Fatalf("IssueCredential: %v", err)
	}
	if !strings.HasPrefix(credential, "im1.") || len(strings.Split(credential, ".")) != 3 {
		t.Fatalf("malformed credential: %q", credential)
	}
	if delta := time.Until(expiresAt); delta < 59*time.Minute || delta > time.Hour {
		t.Fatalf("expiresAt %s is not ~1 hour out", expiresAt)
	}

	user, err := auth.UserFromHeader("Bearer " + credential)
	if err != nil || user == nil {
		t.Fatalf("minted credential does not authenticate: user=%v, err=%v", user, err)
	}
	if user.UUID != "host-7" || user.TokenVersion != 1 {
		t.Fatalf("unexpected user: %+v", user)
	}

	if _, found, err := auth.RevokeUser("host-7"); err != nil || !found {
		t.Fatalf("RevokeUser: found=%v, err=%v", found, err)
	}
	if user, err := auth.UserFromHeader("Bearer " + credential); err != nil || user != nil {
		t.Fatalf("revoked credential still authenticates: user=%v, err=%v", user, err)
	}
}

func TestIssueCredentialAppliesDefaultTTL(t *testing.T) {
	t.Setenv("IM_AUTH_SECRET", "test-auth-secret")
	setupIssueTestDB(t)

	_, expiresAt, err := IssueCredential("host-8", "bob", "", "", 0)
	if err != nil {
		t.Fatalf("IssueCredential: %v", err)
	}
	if delta := time.Until(expiresAt); delta < DefaultCredentialTTL-time.Minute || delta > DefaultCredentialTTL {
		t.Fatalf("expiresAt %s does not reflect the %s default", expiresAt, DefaultCredentialTTL)
	}
}

func TestIssueCredentialRejectsBadInput(t *testing.T) {
	t.Setenv("IM_AUTH_SECRET", "test-auth-secret")
	setupIssueTestDB(t)

	if _, _, err := IssueCredential("host-7", "alice", "", "", MaxCredentialTTL+time.Second); err == nil {
		t.Fatal("ttl above MaxCredentialTTL: got nil error, want failure")
	}
	if _, _, err := IssueCredential("host-7", "alice", "", "", -time.Hour); err == nil {
		t.Fatal("negative ttl: got nil error, want failure")
	}
	if _, _, err := IssueCredential("", "alice", "", "", 0); err == nil {
		t.Fatal("empty uuid: got nil error, want failure")
	}
}

func TestIssueCredentialFailsWithoutAuthSecret(t *testing.T) {
	t.Setenv("IM_AUTH_SECRET", "")

	if _, _, err := IssueCredential("host-7", "alice", "", "", 0); err == nil {
		t.Fatal("IssueCredential without IM_AUTH_SECRET: got nil error, want failure")
	} else if !strings.Contains(err.Error(), "IM_AUTH_SECRET") {
		t.Fatalf("error %q does not name IM_AUTH_SECRET", err)
	}
}

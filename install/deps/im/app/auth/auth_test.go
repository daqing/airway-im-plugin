package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/daqing/airway-im-plugin/install/deps/im/app/repo"
)

const testSecret = "unit-test-signing-secret"

func setupAuthTest(t *testing.T) {
	t.Helper()
	t.Setenv("IM_AUTH_SECRET", testSecret)
	t.Setenv("IM_AUTH_SECRET_PREVIOUS", "")

	db, err := repo.SetupDB("sqlite://:memory:")
	if err != nil {
		t.Fatalf("setup database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`CREATE TABLE users (
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

func mint(t *testing.T, secret, uuid, name, nickname, avatarURL string, ttl time.Duration) string {
	t.Helper()
	credential, err := MintCredential(secret, uuid, name, nickname, avatarURL, ttl)
	if err != nil {
		t.Fatalf("mint credential: %v", err)
	}
	return credential
}

// expiredCredential builds a correctly signed credential whose exp is in the
// past; MintCredential cannot express that through its ttl parameter.
func expiredCredential(t *testing.T, uuid, name string) string {
	t.Helper()
	now := time.Now().UTC()
	payload, err := json.Marshal(credentialClaims{
		UUID:      uuid,
		Name:      name,
		IssuedAt:  now.Add(-2 * time.Hour).Unix(),
		ExpiresAt: now.Add(-time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("marshal expired claims: %v", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return credentialPrefix + "." + encoded + "." + sign(testSecret, encoded)
}

func TestUserFromHeaderRegistersUserOnFirstAuthentication(t *testing.T) {
	setupAuthTest(t)
	credential := mint(t, testSecret, "host-user-42", "alice", "Alice", "https://cdn.example.invalid/a.png", time.Hour)

	user, err := UserFromHeader("Bearer " + credential)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if user == nil {
		t.Fatal("expected a user, got nil")
	}
	if user.UUID != "host-user-42" || user.Username != "alice" {
		t.Fatalf("unexpected identity: %#v", user)
	}
	if user.Nickname == nil || *user.Nickname != "Alice" || user.AvatarURL == nil || *user.AvatarURL != "https://cdn.example.invalid/a.png" {
		t.Fatalf("display fields not stored: %#v", user)
	}

	var count int
	if err := repo.CurrentDB().Get(&count, "SELECT COUNT(*) FROM users WHERE uuid = 'host-user-42'"); err != nil || count != 1 {
		t.Fatalf("stored rows = %d, err = %v", count, err)
	}
}

func TestVerifyCredentialRejectsTamperingAndExpiry(t *testing.T) {
	setupAuthTest(t)

	valid := mint(t, testSecret, "u1", "alice", "", "", time.Hour)
	if _, err := VerifyCredential(valid); err != nil {
		t.Fatalf("valid credential rejected: %v", err)
	}

	tampered := []string{
		"",
		"garbage",
		"im2." + strings.Join(strings.Split(valid, ".")[1:], "."),                  // wrong prefix
		valid[:len(valid)-2] + "AA",                                                // broken signature
		mint(t, "a-completely-different-secret", "u1", "alice", "", "", time.Hour), // wrong secret
		expiredCredential(t, "u1", "alice"),                                        // expired
	}
	for _, credential := range tampered {
		if claims, err := VerifyCredential(credential); err == nil || claims != nil {
			t.Fatalf("expected rejection for %q, got claims=%v err=%v", credential, claims, err)
		}
		if user, err := UserFromHeader("Bearer " + credential); err != nil || user != nil {
			t.Fatalf("expected no user for tampered credential, got %#v err=%v", user, err)
		}
	}
}

func TestRotationAcceptsPreviousSecret(t *testing.T) {
	setupAuthTest(t)
	oldCredential := mint(t, "the-previous-secret", "u1", "alice", "", "", time.Hour)

	t.Setenv("IM_AUTH_SECRET", "the-new-secret")
	t.Setenv("IM_AUTH_SECRET_PREVIOUS", "the-previous-secret")

	if _, err := VerifyCredential(oldCredential); err != nil {
		t.Fatalf("credential signed with previous secret rejected: %v", err)
	}

	t.Setenv("IM_AUTH_SECRET_PREVIOUS", "")
	if _, err := VerifyCredential(oldCredential); err == nil {
		t.Fatal("credential accepted after previous secret was dropped")
	}
}

func TestVersionedCredentialsAreRevocable(t *testing.T) {
	setupAuthTest(t)

	credential, user, err := IssueUserCredential(testSecret, "u-revoke", "carol", "", "", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if user.TokenVersion != 1 {
		t.Fatalf("initial token_version = %d", user.TokenVersion)
	}
	if got, err := UserFromHeader("Bearer " + credential); err != nil || got == nil {
		t.Fatalf("versioned credential rejected: user=%v err=%v", got, err)
	}

	updated, found, err := RevokeUser("u-revoke")
	if err != nil || !found {
		t.Fatalf("revoke: found=%v err=%v", found, err)
	}
	if updated.TokenVersion != 2 {
		t.Fatalf("token_version after revoke = %d", updated.TokenVersion)
	}
	if got, err := UserFromHeader("Bearer " + credential); err != nil || got != nil {
		t.Fatalf("revoked credential still accepted: user=%v err=%v", got, err)
	}

	// Unversioned host-minted credentials are unaffected by revocation.
	plain := mint(t, testSecret, "u-revoke", "carol", "", "", time.Hour)
	if got, err := UserFromHeader("Bearer " + plain); err != nil || got == nil {
		t.Fatalf("unversioned credential rejected after revoke: user=%v err=%v", got, err)
	}

	// A freshly issued credential carries the new version and works again.
	fresh, _, err := IssueUserCredential(testSecret, "u-revoke", "carol", "", "", time.Hour)
	if err != nil {
		t.Fatalf("re-issue: %v", err)
	}
	if got, err := UserFromHeader("Bearer " + fresh); err != nil || got == nil {
		t.Fatalf("fresh credential rejected: user=%v err=%v", got, err)
	}

	if _, found, err := RevokeUser("nobody"); err != nil || found {
		t.Fatalf("revoking unknown user: found=%v err=%v", found, err)
	}
}

func TestResolveRefreshesNameButPreservesAbsentDisplayFields(t *testing.T) {
	setupAuthTest(t)

	first := mint(t, testSecret, "u1", "alice", "Alice", "https://cdn.example.invalid/a.png", time.Hour)
	if _, err := UserFromHeader("Bearer " + first); err != nil {
		t.Fatalf("first login: %v", err)
	}

	// Minimal credential: name changed, no display fields. The stored
	// nickname and avatar must survive; the username must refresh.
	second := mint(t, testSecret, "u1", "alice-new", "", "", time.Hour)
	user, err := UserFromHeader("Bearer " + second)
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	if user.Username != "alice-new" {
		t.Fatalf("username not refreshed: %q", user.Username)
	}
	if user.Nickname == nil || *user.Nickname != "Alice" || user.AvatarURL == nil {
		t.Fatalf("display fields clobbered by minimal credential: %#v", user)
	}

	var username string
	db := repo.CurrentDB()
	if err := db.Get(&username, "SELECT username FROM users WHERE uuid = 'u1'"); err != nil || username != "alice-new" {
		t.Fatalf("stored username = %q, err = %v", username, err)
	}
}

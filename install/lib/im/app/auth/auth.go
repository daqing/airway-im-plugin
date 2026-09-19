// Package auth implements the plugin's credential-based identity. Host
// applications already run their own account systems, so the plugin never
// manages passwords: a host backend signs the pair (name, uuid) — plus
// optional display fields — into a compact HMAC credential and hands it to
// the client. Verifying that credential registers the user on first sight
// and refreshes its profile on later logins.
//
// Credential format (see deps/im/docs/design/identity.md):
//
//	im1.<base64url(payload JSON)>.<base64url(HMAC-SHA256(secret, "im1.".<payload>))>
//
// The signing secret is configured through IM_AUTH_SECRET, with
// IM_AUTH_SECRET_PREVIOUS accepted alongside it for zero-downtime rotation.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/models"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/repo"
)

const (
	credentialPrefix = "im1"

	maxUUIDLength   = 64
	maxNameLength   = 64
	maxAvatarLength = 2048

	// clockSkew tolerates small time differences between the signing host
	// and this backend when validating iat/exp.
	clockSkew = time.Minute

	// lastSeenInterval throttles the last_seen_at refresh so that
	// authenticating on every HTTP request does not turn into a write on
	// every HTTP request.
	lastSeenInterval = 5 * time.Minute
)

// credentialClaims is the signed payload carried inside a credential.
type credentialClaims struct {
	UUID      string `json:"uuid"`
	Name      string `json:"name"`
	Nickname  string `json:"nickname,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
	// TokenVersion binds the credential to the user's current revocation
	// counter. Zero means the claim is absent (host-minted credentials may
	// omit it); a non-zero value must match users.token_version.
	TokenVersion int64 `json:"token_version,omitempty"`
	IssuedAt     int64 `json:"iat"`
	ExpiresAt    int64 `json:"exp,omitempty"`
}

// Bearer extracts a bearer credential without logging or otherwise exposing it.
func Bearer(header string) (string, bool) {
	parts := strings.Fields(header)
	returnToken := len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && parts[1] != ""
	if !returnToken {
		return "", false
	}
	return parts[1], true
}

// MintCredential signs the identity tuple for a client of the host
// application. ttl <= 0 omits the expiry claim (not recommended); callers
// should pass the default of 24 hours unless the host has shorter-lived
// sessions. The credential carries no token_version, so it is not affected
// by per-user revocation; use IssueUserCredential for revocable credentials.
func MintCredential(secret, uuid, name, nickname, avatarURL string, ttl time.Duration) (string, error) {
	if secret == "" {
		return "", errors.New("auth: credential signing secret is not configured")
	}
	if err := validateClaims(uuid, name, nickname, avatarURL); err != nil {
		return "", err
	}
	return mintClaims(secret, credentialClaims{UUID: uuid, Name: name, Nickname: nickname, AvatarURL: avatarURL}, ttl)
}

// IssueUserCredential resolves the host identity against the users table
// (registering it on first sight) and mints a credential carrying the user's
// current token_version. Bumping that version through RevokeUser invalidates
// every credential minted this way.
func IssueUserCredential(secret, uuid, name, nickname, avatarURL string, ttl time.Duration) (string, *models.User, error) {
	if secret == "" {
		return "", nil, errors.New("auth: credential signing secret is not configured")
	}
	if err := validateClaims(uuid, name, nickname, avatarURL); err != nil {
		return "", nil, err
	}
	user, err := resolveUser(&credentialClaims{UUID: uuid, Name: name, Nickname: nickname, AvatarURL: avatarURL})
	if err != nil {
		return "", nil, err
	}
	credential, err := mintClaims(secret, credentialClaims{
		UUID:         user.UUID,
		Name:         user.Username,
		Nickname:     nickname,
		AvatarURL:    avatarURL,
		TokenVersion: user.TokenVersion,
	}, ttl)
	if err != nil {
		return "", nil, err
	}
	return credential, user, nil
}

// RevokeUser bumps the user's token_version, immediately invalidating every
// versioned credential minted so far. found is false when no user exists for
// the uuid. Unversioned (host-minted) credentials are unaffected.
func RevokeUser(uuid string) (user *models.User, found bool, err error) {
	db := repo.CurrentDB()
	result, err := db.Exec(db.Rebind("UPDATE users SET token_version = token_version + 1, updated_at = ? WHERE uuid = ?"), time.Now().UTC(), uuid)
	if err != nil {
		return nil, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if affected == 0 {
		return nil, false, nil
	}
	var updated models.User
	if err := db.Get(&updated, db.Rebind("SELECT * FROM users WHERE uuid = ?"), uuid); err != nil {
		return nil, false, err
	}
	return &updated, true, nil
}

func mintClaims(secret string, claims credentialClaims, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	claims.IssuedAt = now.Unix()
	if ttl > 0 {
		claims.ExpiresAt = now.Add(ttl).Unix()
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signature := sign(secret, encodedPayload)
	return credentialPrefix + "." + encodedPayload + "." + signature, nil
}

// UserFromHeader authenticates a standard Authorization header. A nil user
// without error means the credential is missing, malformed, or invalid;
// non-nil errors indicate backend failures.
func UserFromHeader(header string) (*models.User, error) {
	credential, ok := Bearer(header)
	if !ok {
		return nil, nil
	}
	claims, err := VerifyCredential(credential)
	if err != nil || claims == nil {
		return nil, nil
	}
	user, err := resolveUser(claims)
	if err != nil || user == nil {
		return user, err
	}
	if claims.TokenVersion != 0 && claims.TokenVersion != user.TokenVersion {
		return nil, nil
	}
	return user, nil
}

// VerifyCredential checks the signature and time window of a credential and
// returns its claims.
func VerifyCredential(credential string) (*credentialClaims, error) {
	parts := strings.Split(credential, ".")
	if len(parts) != 3 || parts[0] != credentialPrefix {
		return nil, errors.New("auth: malformed credential")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("auth: malformed credential payload")
	}

	matched := false
	for _, secret := range authSecrets() {
		if hmac.Equal([]byte(sign(secret, parts[1])), []byte(parts[2])) {
			matched = true
			break
		}
	}
	if !matched {
		return nil, errors.New("auth: invalid credential signature")
	}

	var claims credentialClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, errors.New("auth: invalid credential payload")
	}
	if err := validateClaims(claims.UUID, claims.Name, claims.Nickname, claims.AvatarURL); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if claims.IssuedAt != 0 && time.Unix(claims.IssuedAt, 0).After(now.Add(clockSkew)) {
		return nil, errors.New("auth: credential issued in the future")
	}
	if claims.ExpiresAt != 0 && now.After(time.Unix(claims.ExpiresAt, 0).Add(clockSkew)) {
		return nil, errors.New("auth: credential expired")
	}
	return &claims, nil
}

// authSecrets returns the accepted signing secrets: the current one plus the
// previous one during rotation. They are read per call so rotation takes
// effect without a restart.
func authSecrets() []string {
	secrets := make([]string, 0, 2)
	for _, name := range []string{"IM_AUTH_SECRET", "IM_AUTH_SECRET_PREVIOUS"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			secrets = append(secrets, value)
		}
	}
	return secrets
}

func sign(secret, encodedPayload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(credentialPrefix + "." + encodedPayload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func validateClaims(uuid, name, nickname, avatarURL string) error {
	uuid = strings.TrimSpace(uuid)
	name = strings.TrimSpace(name)
	if uuid == "" || len(uuid) > maxUUIDLength {
		return errors.New("auth: credential uuid must be 1-64 characters")
	}
	if name == "" || len(name) > maxNameLength {
		return errors.New("auth: credential name must be 1-64 characters")
	}
	if len(nickname) > maxNameLength || len(avatarURL) > maxAvatarLength {
		return errors.New("auth: credential display fields are too long")
	}
	return nil
}

// resolveUser maps verified claims onto the users table, registering the
// user on first authentication. The name always refreshes when it changes;
// optional display fields refresh only when the credential carries them, so
// a minimal credential never clobbers a richer stored profile.
func resolveUser(claims *credentialClaims) (*models.User, error) {
	db := repo.CurrentDB()
	var user models.User
	err := db.Get(&user, db.Rebind("SELECT * FROM users WHERE uuid = ?"), claims.UUID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		now := time.Now().UTC()
		result, insertErr := db.Exec(db.Rebind("INSERT INTO users (uuid, username, nickname, avatar_url, last_seen_at, token_version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)"), claims.UUID, claims.Name, nilOrNULL(claims.Nickname), nilOrNULL(claims.AvatarURL), now, 1, now, now)
		if insertErr != nil {
			// A concurrent first login may have inserted the same uuid;
			// fall back to reading the winning row.
			if lookupErr := db.Get(&user, db.Rebind("SELECT * FROM users WHERE uuid = ?"), claims.UUID); lookupErr == nil {
				return &user, nil
			}
			return nil, insertErr
		}
		id, _ := result.LastInsertId()
		if id == 0 {
			if lookupErr := db.Get(&user, db.Rebind("SELECT * FROM users WHERE uuid = ?"), claims.UUID); lookupErr == nil {
				return &user, nil
			}
		}
		return &models.User{
			ID:           id,
			UUID:         claims.UUID,
			Username:     claims.Name,
			Nickname:     nilOrPtr(claims.Nickname),
			AvatarURL:    nilOrPtr(claims.AvatarURL),
			LastSeenAt:   &now,
			TokenVersion: 1,
		}, nil
	case err != nil:
		return nil, err
	}

	refreshName := user.Username != claims.Name
	refreshProfile := (claims.Nickname != "" && (user.Nickname == nil || *user.Nickname != claims.Nickname)) ||
		(claims.AvatarURL != "" && (user.AvatarURL == nil || *user.AvatarURL != claims.AvatarURL))
	refreshSeen := user.LastSeenAt == nil || time.Since(*user.LastSeenAt) > lastSeenInterval
	if refreshName || refreshProfile || refreshSeen {
		next := user
		if refreshName {
			next.Username = claims.Name
		}
		if claims.Nickname != "" {
			next.Nickname = &claims.Nickname
		}
		if claims.AvatarURL != "" {
			next.AvatarURL = &claims.AvatarURL
		}
		now := time.Now().UTC()
		if _, updateErr := db.Exec(db.Rebind("UPDATE users SET username = ?, nickname = ?, avatar_url = ?, last_seen_at = ?, updated_at = ? WHERE id = ?"), next.Username, stringOrNULL(next.Nickname), stringOrNULL(next.AvatarURL), now, now, user.ID); updateErr != nil {
			return &user, nil
		}
		next.LastSeenAt = &now
		next.UpdatedAt = now
		return &next, nil
	}
	return &user, nil
}

func nilOrNULL(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nilOrPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func stringOrNULL(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

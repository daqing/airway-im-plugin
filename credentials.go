package implugin

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/auth"
)

const (
	// DefaultCredentialTTL applies when IssueCredential is called with a
	// zero ttl. It matches the internal minting API's default.
	DefaultCredentialTTL = 24 * time.Hour

	// MaxCredentialTTL caps the ttl accepted by IssueCredential, matching
	// the internal minting API's limit.
	MaxCredentialTTL = 30 * 24 * time.Hour
)

// IssueCredential mints a revocable client credential for a host user
// in-process — Go hosts embedding this plugin call it during their own
// login flow instead of POSTing to /internal/v1/credentials, saving the
// HTTP round trip. The signing secret is read from IM_AUTH_SECRET, the
// user is registered (or refreshed) in the users table at mint time, and
// the credential carries the user's current token_version, so it can be
// revoked through the admin API. A zero ttl applies DefaultCredentialTTL;
// negative or above MaxCredentialTTL is rejected. Requires the framework
// database to be set up (any point after boot).
func IssueCredential(uuid, name, nickname, avatarURL string, ttl time.Duration) (credential string, expiresAt time.Time, err error) {
	secret := strings.TrimSpace(os.Getenv("IM_AUTH_SECRET"))
	if secret == "" {
		return "", time.Time{}, errors.New("implugin: IM_AUTH_SECRET is not configured")
	}
	if ttl == 0 {
		ttl = DefaultCredentialTTL
	}
	if ttl < 0 || ttl > MaxCredentialTTL {
		return "", time.Time{}, fmt.Errorf("implugin: credential ttl must be between 0 and %s (0 applies the %s default)", MaxCredentialTTL, DefaultCredentialTTL)
	}
	credential, _, err = auth.IssueUserCredential(secret, strings.TrimSpace(uuid), strings.TrimSpace(name), strings.TrimSpace(nickname), strings.TrimSpace(avatarURL), ttl)
	if err != nil {
		return "", time.Time{}, err
	}
	return credential, time.Now().UTC().Add(ttl), nil
}

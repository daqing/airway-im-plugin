package admin_api

import (
	"net/http"
	"strings"
	"time"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/moderation"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/repo"
	"github.com/gin-gonic/gin"
)

type adminUser struct {
	UUID         string            `db:"uuid" json:"uuid"`
	Username     string            `db:"username" json:"username"`
	Nickname     *string           `db:"nickname" json:"nickname"`
	AvatarURL    *string           `db:"avatar_url" json:"avatar_url"`
	Email        *string           `db:"email" json:"email"`
	LastSeenAt   *time.Time        `db:"last_seen_at" json:"last_seen_at"`
	TokenVersion int64             `db:"token_version" json:"token_version"`
	Labels       moderation.Labels `db:"labels" json:"labels"`
	CreatedAt    time.Time         `db:"created_at" json:"created_at"`
}

// ListUsers returns every identity registered with the IM service, newest
// first. Rows are created automatically the first time a host-signed
// credential for a (name, uuid) pair authenticates; last_seen_at shows the
// most recent authenticated activity (throttled to five-minute granularity).
//
// An optional `label` query parameter narrows the listing to identities
// carrying that moderation label, e.g. `?label=banned` for permanently
// banned accounts. The rows keep the newest-first order.
func ListUsers(c *gin.Context) {
	db := repo.CurrentDB()
	var users []adminUser
	query := "SELECT uuid, username, nickname, avatar_url, email, last_seen_at, token_version, labels, created_at FROM users ORDER BY created_at DESC"
	if err := db.Select(&users, query); err != nil {
		adminError(c, http.StatusInternalServerError, 20000, "Could not load users")
		return
	}
	if label := strings.TrimSpace(c.Query("label")); label != "" {
		filtered := users[:0]
		for _, user := range users {
			if user.Labels.Has(label) {
				filtered = append(filtered, user)
			}
		}
		users = filtered
	}
	if users == nil {
		users = []adminUser{}
	}
	adminOK(c, users)
}

package internal_api

import (
	"crypto/subtle"
	"database/sql"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/auth"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/repo"
	"github.com/gin-gonic/gin"
)

type outboxEvent struct {
	ID          string         `db:"id" json:"id"`
	Topic       string         `db:"topic" json:"topic"`
	AggregateID string         `db:"aggregate_id" json:"aggregate_id"`
	Payload     string         `db:"payload" json:"-"`
	CreatedAt   time.Time      `db:"created_at" json:"created_at"`
	PublishedAt sql.NullTime   `db:"published_at" json:"-"`
	Attempts    int            `db:"attempts" json:"attempts"`
	Data        map[string]any `json:"payload"`
}

// issueCredentialRequest is the server-to-server body for minting a client
// credential on behalf of a host application's user.
type issueCredentialRequest struct {
	UUID       string  `json:"uuid"`
	Name       string  `json:"name"`
	Nickname   *string `json:"nickname"`
	AvatarURL  *string `json:"avatar_url"`
	TTLSeconds *int64  `json:"ttl_seconds"`
}

const (
	defaultCredentialTTLSeconds = 24 * 60 * 60
	maxCredentialTTLSeconds     = 30 * 24 * 60 * 60
)

func Authenticate(c *gin.Context) {
	if !internalAuthorized(c) {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 10005, "data": nil, "message": "Internal authentication required"})
		return
	}
	user, err := auth.UserFromHeader(c.GetHeader("Authorization"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 10000, "data": nil, "message": "Authentication service failed"})
		return
	}
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 10001, "data": nil, "message": "Invalid bearer token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"user_id": user.ID}, "message": nil})
}

// IssueCredential mints a client credential for a host application's user.
// Host backends that do not want to implement the HMAC locally call this
// during their own login flow and hand the returned credential to the
// client. The user is registered on first mint, and the credential carries
// the user's current token_version so it can be revoked through the admin
// API. See deps/im/docs/design/identity.md for the format specification.
func IssueCredential(c *gin.Context) {
	if !internalAuthorized(c) {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 10005, "data": nil, "message": "Internal authentication required"})
		return
	}
	secret := strings.TrimSpace(os.Getenv("IM_AUTH_SECRET"))
	if secret == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 10006, "data": nil, "message": "Credential signing is not configured"})
		return
	}
	var request issueCredentialRequest
	if err := decodeJSONBody(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 10003, "data": nil, "message": "Invalid JSON body"})
		return
	}
	request.UUID = strings.TrimSpace(request.UUID)
	request.Name = strings.TrimSpace(request.Name)
	if request.UUID == "" || len(request.UUID) > 64 || request.Name == "" || len(request.Name) > 64 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 10003, "data": nil, "message": "uuid and name are required (1-64 characters each)"})
		return
	}
	ttlSeconds := int64(defaultCredentialTTLSeconds)
	if request.TTLSeconds != nil {
		if *request.TTLSeconds < 0 || *request.TTLSeconds > maxCredentialTTLSeconds {
			c.JSON(http.StatusBadRequest, gin.H{"code": 10003, "data": nil, "message": "ttl_seconds must be between 0 and 2592000"})
			return
		}
		ttlSeconds = *request.TTLSeconds
	}
	credential, _, err := auth.IssueUserCredential(secret, request.UUID, request.Name, trimOptional(request.Nickname), trimOptional(request.AvatarURL), time.Duration(ttlSeconds)*time.Second)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 10000, "data": nil, "message": "Could not issue credential"})
		return
	}
	data := gin.H{"credential": credential}
	if ttlSeconds > 0 {
		data["expires_at"] = time.Now().UTC().Add(time.Duration(ttlSeconds) * time.Second)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": data, "message": nil})
}

func trimOptional(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func Outbox(c *gin.Context) {
	if !internalAuthorized(c) {
		c.Status(http.StatusUnauthorized)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit < 1 || limit > 500 {
		limit = 100
	}
	db := repo.CurrentDB()
	var events []outboxEvent
	query := "SELECT id, topic, aggregate_id, payload, created_at, published_at, attempts FROM outbox_events WHERE published_at IS NULL ORDER BY created_at ASC LIMIT ?"
	if err := db.Select(&events, db.Rebind(query), limit); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 10000, "data": nil, "message": "Could not load outbox"})
		return
	}
	for index := range events {
		if err := decodeJSON(events[index].Payload, &events[index].Data); err != nil {
			events[index].Data = map[string]any{"invalid_payload": true}
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": events, "message": nil})
}

func AckOutbox(c *gin.Context) {
	if !internalAuthorized(c) {
		c.Status(http.StatusUnauthorized)
		return
	}
	db := repo.CurrentDB()
	result, err := db.Exec(db.Rebind("UPDATE outbox_events SET published_at = ?, attempts = attempts + 1 WHERE id = ? AND published_at IS NULL"), time.Now().UTC(), c.Param("id"))
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.Status(http.StatusNotFound)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": "OK", "message": nil})
}

func internalAuthorized(c *gin.Context) bool {
	want := strings.TrimSpace(os.Getenv("IM_INTERNAL_SECRET"))
	got := c.GetHeader("X-IM-Internal-Secret")
	return want != "" && len(want) == len(got) && subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

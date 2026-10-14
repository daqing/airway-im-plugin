package im_api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/models"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/moderation"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/repo"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/services"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/utils"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
)

// maxLabels and maxLabelLength bound what a client may report as its own
// screening verdict; labels are a small fixed vocabulary, not free text.
const (
	maxLabels      = 16
	maxLabelLength = 32
)

type createMessageRequest struct {
	ConversationID string            `json:"conversation_id"`
	Content        string            `json:"content"`
	ContentType    string            `json:"content_type"`
	Labels         moderation.Labels `json:"labels"`
}

type createConversationMessageRequest struct {
	Content     string            `json:"content"`
	ContentType string            `json:"content_type"`
	Labels      moderation.Labels `json:"labels"`
}

type senderResponse struct {
	UUID      string  `db:"sender_uuid" json:"uuid"`
	Username  string  `db:"username" json:"username"`
	Nickname  *string `db:"nickname" json:"nickname"`
	AvatarURL *string `db:"avatar_url" json:"avatar_url"`
}

type messageResponse struct {
	ID             string         `db:"id" json:"id"`
	ConversationID string         `db:"conversation_id" json:"conversation_id"`
	Sender         senderResponse `json:"sender"`
	Content        string         `db:"content" json:"content"`
	ContentType    string         `db:"content_type" json:"content_type"`
	CreatedAt      time.Time      `db:"created_at" json:"created_at"`
	Sequence       int64          `db:"sequence" json:"sequence"`
	// ReviewStatus is set only on a queued response: the message is held for
	// human review, carries sequence 0, and has not reached any member.
	ReviewStatus string `json:"review_status,omitempty"`
}

type messageRow struct {
	ID             string    `db:"id"`
	ConversationID string    `db:"conversation_id"`
	SenderUUID     string    `db:"sender_uuid"`
	Username       string    `db:"username"`
	Nickname       *string   `db:"nickname"`
	AvatarURL      *string   `db:"avatar_url"`
	Content        string    `db:"content"`
	ContentType    string    `db:"content_type"`
	CreatedAt      time.Time `db:"created_at"`
	Sequence       int64     `db:"sequence"`
	IsIllegal      bool      `db:"is_illegal"`
}

func CreateMessage(c *gin.Context) {
	user, ok := currentUser(c)
	if !ok {
		return
	}
	var request createMessageRequest
	if err := decodeJSONBody(c.Request.Body, &request); err != nil {
		respondError(c, http.StatusBadRequest, 10003, "Invalid JSON body")
		return
	}
	createMessage(c, user, request)
}

func CreateConversationMessage(c *gin.Context) {
	user, ok := currentUser(c)
	if !ok {
		return
	}
	var request createConversationMessageRequest
	if err := decodeJSONBody(c.Request.Body, &request); err != nil {
		respondError(c, http.StatusBadRequest, 10003, "Invalid JSON body")
		return
	}
	createMessage(c, user, createMessageRequest{
		ConversationID: c.Param("conversation_uuid"),
		Content:        request.Content,
		ContentType:    request.ContentType,
		Labels:         request.Labels,
	})
}

func createMessage(c *gin.Context, user *models.User, request createMessageRequest) {
	request.ConversationID = strings.TrimSpace(request.ConversationID)
	request.Content = strings.ReplaceAll(request.Content, "\r\n", "\n")
	request.Content = strings.ReplaceAll(request.Content, "\r", "\n")
	if request.ContentType == "" {
		request.ContentType = "text/markdown"
	}
	if len(request.ConversationID) != 26 || strings.TrimSpace(request.Content) == "" || !utf8.ValidString(request.Content) || len(request.Content) > 32*1024 || (request.ContentType != "text/markdown" && request.ContentType != "text/plain") {
		respondError(c, http.StatusBadRequest, 10003, "Invalid message fields")
		return
	}
	labels, err := normalizeLabels(request.Labels)
	if err != nil {
		respondError(c, http.StatusBadRequest, 10003, "Invalid message fields")
		return
	}
	request.Labels = labels

	db := repo.CurrentDB()
	member, err := activeMember(db, request.ConversationID, user.ID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not authorize message")
		return
	}
	if !member {
		respondError(c, http.StatusForbidden, 10005, "Permission denied")
		return
	}

	hash := requestHash(request)
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key != "" {
		if len(key) > 128 {
			respondError(c, http.StatusBadRequest, 10003, "Idempotency key is too long")
			return
		}
		storedHash, messageID, found, lookupErr := lookupIdempotency(db, user.ID, key)
		if lookupErr != nil {
			respondError(c, http.StatusInternalServerError, 10000, "Could not process idempotency key")
			return
		}
		if found {
			if storedHash != hash {
				respondError(c, http.StatusConflict, 11002, "Idempotency key was reused with a different request")
				return
			}
			message, loadErr := loadMessage(db, messageID)
			if loadErr != nil && !errors.Is(loadErr, sql.ErrNoRows) {
				respondError(c, http.StatusInternalServerError, 10000, "Could not load message")
				return
			}
			if loadErr == nil {
				respond(c, http.StatusOK, message)
				return
			}
			// The key may also point at a queued message: nothing was
			// delivered, so the retry replays the same review record.
			queued, reviewErr := loadReviewedMessage(db, messageID)
			if reviewErr != nil || queued == nil {
				respondError(c, http.StatusInternalServerError, 10000, "Could not load message")
				return
			}
			respond(c, http.StatusOK, queued)
			return
		}
	}

	messageID, err := utils.NewULID()
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not create message")
		return
	}
	now := time.Now().UTC()

	// The moderation service owns the word list. It failing is not a pass:
	// the message is held for review, so nothing is delivered unscreened.
	screen, screenErr := moderation.ScreenContent(c.Request.Context(), request.Content)
	flagged := screenErr != nil || screen.Flagged
	flagMatches := screen.Words
	if screenErr != nil {
		flagMatches = []string{moderation.ScreeningUnavailable}
	}
	restricted, err := moderation.UserHasLabel(db, user.ID, moderation.LabelIllegal)
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not create message")
		return
	}
	if flagged || restricted || request.Labels.Has(moderation.LabelIllegal) {
		if err := enqueueReview(db, user, request, messageID, flagged, flagMatches, hash, key, now); err != nil {
			respondError(c, http.StatusInternalServerError, 10000, "Could not create message")
			return
		}
		respond(c, http.StatusCreated, messageResponse{ID: messageID, ConversationID: request.ConversationID, Sender: senderResponse{UUID: user.UUID, Username: user.Username, Nickname: user.Nickname, AvatarURL: user.AvatarURL}, Content: request.Content, ContentType: request.ContentType, CreatedAt: now, ReviewStatus: moderation.ReviewPending})
		return
	}

	var sequence int64
	err = repo.Tx(db, func(tx *sqlx.Tx) error {
		if key != "" {
			if _, err := tx.Exec(tx.Rebind("INSERT INTO message_idempotency (user_id, idempotency_key, request_hash, message_id, created_at) VALUES (?, ?, ?, ?, ?)"), user.ID, key, hash, messageID, now); err != nil {
				return err
			}
		}
		var err error
		sequence, err = services.ReleaseMessage(tx, request.ConversationID, messageID, user.ID, request.Content, request.ContentType, now)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		respondError(c, http.StatusNotFound, 11001, "Conversation not found")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not create message")
		return
	}
	respond(c, http.StatusCreated, messageResponse{ID: messageID, ConversationID: request.ConversationID, Sender: senderResponse{UUID: user.UUID, Username: user.Username, Nickname: user.Nickname, AvatarURL: user.AvatarURL}, Content: request.Content, ContentType: request.ContentType, CreatedAt: now, Sequence: sequence})
}

// enqueueReview parks a flagged message in message_reviews. The record id is
// the id the message takes if it is approved, so a client can dedupe its
// "pending" bubble against the delivered message, and nothing reaches a
// member until an operator decides. flagMatches records what the screening
// service found, so an operator reads why the message was held.
func enqueueReview(db *sqlx.DB, user *models.User, request createMessageRequest, messageID string, serverFlagged bool, flagMatches []string, hash, key string, now time.Time) error {
	flagLabels := request.Labels
	if serverFlagged {
		flagLabels = flagLabels.With(moderation.LabelIllegal)
	}
	encoded, err := flagLabels.JSON()
	if err != nil {
		return err
	}
	encodedMatches, err := moderation.Labels(flagMatches).JSON()
	if err != nil {
		return err
	}
	return repo.Tx(db, func(tx *sqlx.Tx) error {
		if _, err := tx.Exec(tx.Rebind("INSERT INTO message_reviews (id, conversation_id, sender_id, content, content_type, flag_labels, flag_matches, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"), messageID, request.ConversationID, user.ID, request.Content, request.ContentType, encoded, encodedMatches, moderation.ReviewPending, now); err != nil {
			return err
		}
		if key != "" {
			if _, err := tx.Exec(tx.Rebind("INSERT INTO message_idempotency (user_id, idempotency_key, request_hash, message_id, created_at) VALUES (?, ?, ?, ?, ?)"), user.ID, key, hash, messageID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func ListMessages(c *gin.Context) {
	user, ok := currentUser(c)
	if !ok {
		return
	}
	conversationID := c.Param("conversation_uuid")
	member, err := activeMember(repo.CurrentDB(), conversationID, user.ID)
	if err != nil || !member {
		respondError(c, http.StatusForbidden, 10005, "Permission denied")
		return
	}
	after, _ := strconv.ParseInt(c.DefaultQuery("after_sequence", "0"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit < 1 || limit > 200 {
		limit = 100
	}
	query := "SELECT m.id, m.conversation_id, u.uuid AS sender_uuid, u.username, u.nickname, u.avatar_url, m.content, m.content_type, m.created_at, m.sequence, m.is_illegal FROM messages m JOIN users u ON u.id = m.sender_id WHERE m.conversation_id = ? AND m.sequence > ? ORDER BY m.sequence ASC LIMIT ?"
	var rows []messageRow
	db := repo.CurrentDB()
	if err := db.Select(&rows, db.Rebind(query), conversationID, after, limit); err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not load messages")
		return
	}
	messages := make([]messageResponse, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, row.response())
	}
	respond(c, http.StatusOK, messages)
}

func (row messageRow) response() messageResponse {
	content := row.Content
	if row.IsIllegal {
		content = "***"
	}
	return messageResponse{ID: row.ID, ConversationID: row.ConversationID, Sender: senderResponse{UUID: row.SenderUUID, Username: row.Username, Nickname: row.Nickname, AvatarURL: row.AvatarURL}, Content: content, ContentType: row.ContentType, CreatedAt: row.CreatedAt, Sequence: row.Sequence}
}

func activeMember(db *sqlx.DB, conversationID string, userID int64) (bool, error) {
	var count int
	err := db.Get(&count, db.Rebind("SELECT COUNT(*) FROM conversation_members WHERE conversation_id = ? AND user_id = ? AND left_at IS NULL"), conversationID, userID)
	return count == 1, err
}

func requestHash(request createMessageRequest) string {
	data, _ := json.Marshal(request)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// normalizeLabels trims and de-dupes the labels a client reports for its own
// message, rejecting a set no real client sends so the queue cannot be filled
// with junk.
func normalizeLabels(labels []string) (moderation.Labels, error) {
	if len(labels) > maxLabels {
		return nil, errors.New("too many labels")
	}
	normalized := moderation.Labels{}
	for _, label := range labels {
		trimmed := strings.TrimSpace(label)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > maxLabelLength {
			return nil, errors.New("label is too long")
		}
		normalized = normalized.With(trimmed)
	}
	return normalized, nil
}

func lookupIdempotency(db *sqlx.DB, userID int64, key string) (string, string, bool, error) {
	var value struct {
		Hash      string `db:"request_hash"`
		MessageID string `db:"message_id"`
	}
	err := db.Get(&value, db.Rebind("SELECT request_hash, message_id FROM message_idempotency WHERE user_id = ? AND idempotency_key = ?"), userID, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return value.Hash, value.MessageID, err == nil, err
}

func loadMessage(db *sqlx.DB, messageID string) (*messageResponse, error) {
	query := "SELECT m.id, m.conversation_id, u.uuid AS sender_uuid, u.username, u.nickname, u.avatar_url, m.content, m.content_type, m.created_at, m.sequence, m.is_illegal FROM messages m JOIN users u ON u.id = m.sender_id WHERE m.id = ?"
	var row messageRow
	if err := db.Get(&row, db.Rebind(query), messageID); err != nil {
		return nil, err
	}
	response := row.response()
	return &response, nil
}

// loadReviewedMessage reads a queued message back in the shape a client sent
// it: sequence 0 and the review status, so a retried send replays the same
// pending (or rejected) response. It returns nil, nil when the id is neither
// a delivered nor a queued message.
func loadReviewedMessage(db *sqlx.DB, messageID string) (*messageResponse, error) {
	var row struct {
		ID             string    `db:"id"`
		ConversationID string    `db:"conversation_id"`
		SenderUUID     string    `db:"sender_uuid"`
		Username       string    `db:"username"`
		Nickname       *string   `db:"nickname"`
		AvatarURL      *string   `db:"avatar_url"`
		Content        string    `db:"content"`
		ContentType    string    `db:"content_type"`
		CreatedAt      time.Time `db:"created_at"`
		Status         string    `db:"status"`
	}
	query := "SELECT r.id, r.conversation_id, u.uuid AS sender_uuid, u.username, u.nickname, u.avatar_url, r.content, r.content_type, r.created_at, r.status FROM message_reviews r JOIN users u ON u.id = r.sender_id WHERE r.id = ?"
	if err := db.Get(&row, db.Rebind(query), messageID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &messageResponse{
		ID:             row.ID,
		ConversationID: row.ConversationID,
		Sender:         senderResponse{UUID: row.SenderUUID, Username: row.Username, Nickname: row.Nickname, AvatarURL: row.AvatarURL},
		Content:        row.Content,
		ContentType:    row.ContentType,
		CreatedAt:      row.CreatedAt,
		ReviewStatus:   row.Status,
	}, nil
}

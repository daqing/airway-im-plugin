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

	"github.com/daqing/airway-im-plugin/app/models"
	"github.com/daqing/airway-im-plugin/app/repo"
	"github.com/daqing/airway-im-plugin/app/utils"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
)

type createMessageRequest struct {
	ConversationID string `json:"conversation_id"`
	Content        string `json:"content"`
	ContentType    string `json:"content_type"`
}

type createConversationMessageRequest struct {
	Content     string `json:"content"`
	ContentType string `json:"content_type"`
}

type senderResponse struct {
	ID        int64   `db:"sender_id" json:"id"`
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
}

type messageRow struct {
	ID             string    `db:"id"`
	ConversationID string    `db:"conversation_id"`
	SenderID       int64     `db:"sender_id"`
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
			if loadErr != nil {
				respondError(c, http.StatusInternalServerError, 10000, "Could not load message")
				return
			}
			respond(c, http.StatusOK, message)
			return
		}
	}

	messageID, err := utils.NewULID()
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not create message")
		return
	}
	eventID, err := utils.NewULID()
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not create message")
		return
	}
	now := time.Now().UTC()
	var sequence int64
	err = repo.Tx(db, func(tx *sqlx.Tx) error {
		result, err := tx.Exec(tx.Rebind("UPDATE conversations SET next_sequence = next_sequence + 1, updated_at = ? WHERE id = ?"), now, request.ConversationID)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return sql.ErrNoRows
		}
		if err := tx.Get(&sequence, tx.Rebind("SELECT next_sequence - 1 FROM conversations WHERE id = ?"), request.ConversationID); err != nil {
			return err
		}
		if _, err := tx.Exec(tx.Rebind("INSERT INTO messages (id, conversation_id, sender_id, content, content_type, sequence, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)"), messageID, request.ConversationID, user.ID, request.Content, request.ContentType, sequence, now); err != nil {
			return err
		}
		if key != "" {
			if _, err := tx.Exec(tx.Rebind("INSERT INTO message_idempotency (user_id, idempotency_key, request_hash, message_id, created_at) VALUES (?, ?, ?, ?, ?)"), user.ID, key, hash, messageID, now); err != nil {
				return err
			}
		}
		var targets []int64
		if err := tx.Select(&targets, tx.Rebind("SELECT user_id FROM conversation_members WHERE conversation_id = ? AND left_at IS NULL"), request.ConversationID); err != nil {
			return err
		}
		payload, err := json.Marshal(gin.H{"event_id": eventID, "event": "message.created", "message_id": messageID, "conversation_id": request.ConversationID, "sequence": sequence, "targets": gin.H{"user_ids": targets}})
		if err != nil {
			return err
		}
		_, err = tx.Exec(tx.Rebind("INSERT INTO outbox_events (id, topic, aggregate_id, payload, created_at, attempts) VALUES (?, 'message.created', ?, ?, ?, 0)"), eventID, messageID, string(payload), now)
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
	respond(c, http.StatusCreated, messageResponse{ID: messageID, ConversationID: request.ConversationID, Sender: senderResponse{ID: user.ID, Username: user.Username, Nickname: user.Nickname, AvatarURL: user.AvatarURL}, Content: request.Content, ContentType: request.ContentType, CreatedAt: now, Sequence: sequence})
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
	query := "SELECT m.id, m.conversation_id, m.sender_id, u.username, u.nickname, u.avatar_url, m.content, m.content_type, m.created_at, m.sequence, m.is_illegal FROM messages m JOIN users u ON u.id = m.sender_id WHERE m.conversation_id = ? AND m.sequence > ? ORDER BY m.sequence ASC LIMIT ?"
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
	return messageResponse{ID: row.ID, ConversationID: row.ConversationID, Sender: senderResponse{ID: row.SenderID, Username: row.Username, Nickname: row.Nickname, AvatarURL: row.AvatarURL}, Content: content, ContentType: row.ContentType, CreatedAt: row.CreatedAt, Sequence: row.Sequence}
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
	query := "SELECT m.id, m.conversation_id, m.sender_id, u.username, u.nickname, u.avatar_url, m.content, m.content_type, m.created_at, m.sequence, m.is_illegal FROM messages m JOIN users u ON u.id = m.sender_id WHERE m.id = ?"
	var row messageRow
	if err := db.Get(&row, db.Rebind(query), messageID); err != nil {
		return nil, err
	}
	response := row.response()
	return &response, nil
}

package admin_api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/repo"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/utils"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
)

type adminConversation struct {
	ID              string    `db:"id" json:"id"`
	Kind            string    `db:"kind" json:"kind"`
	Title           *string   `db:"title" json:"title"`
	AvatarURL       *string   `db:"avatar_url" json:"avatar_url"`
	CreatedBy       string    `db:"created_by" json:"created_by"`
	CreatorUsername string    `db:"creator_username" json:"creator_username"`
	MemberCount     int64     `db:"member_count" json:"member_count"`
	MessageCount    int64     `db:"message_count" json:"message_count"`
	LatestMessageAt *string   `db:"latest_message_at" json:"latest_message_at"`
	CreatedAt       time.Time `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time `db:"updated_at" json:"updated_at"`
}

type adminMessage struct {
	ID              string     `db:"id" json:"id"`
	ConversationID  string     `db:"conversation_id" json:"conversation_id"`
	SenderUUID      string     `db:"sender_uuid" json:"sender_uuid"`
	SenderUsername  string     `db:"sender_username" json:"sender_username"`
	SenderNickname  *string    `db:"sender_nickname" json:"sender_nickname"`
	SenderAvatarURL *string    `db:"sender_avatar_url" json:"sender_avatar_url"`
	Content         string     `db:"content" json:"content"`
	ContentType     string     `db:"content_type" json:"content_type"`
	Sequence        int64      `db:"sequence" json:"sequence"`
	CreatedAt       time.Time  `db:"created_at" json:"created_at"`
	IsIllegal       bool       `db:"is_illegal" json:"is_illegal"`
	ModeratedAt     *time.Time `db:"moderated_at" json:"moderated_at"`
}

func ListConversations(c *gin.Context) {
	db := repo.CurrentDB()
	query := `
		SELECT c.id, c.kind, c.title, c.avatar_url, creator.uuid AS created_by,
		       creator.username AS creator_username,
		       (SELECT COUNT(*) FROM conversation_members cm
		        WHERE cm.conversation_id = c.id AND cm.left_at IS NULL) AS member_count,
		       (SELECT COUNT(*) FROM messages m
		        WHERE m.conversation_id = c.id) AS message_count,
		       (SELECT MAX(m.created_at) FROM messages m
		        WHERE m.conversation_id = c.id) AS latest_message_at,
		       c.created_at, c.updated_at
		FROM conversations c
		JOIN users creator ON creator.id = c.created_by
		WHERE c.kind = 'group'
		ORDER BY c.updated_at DESC, c.id DESC`
	var conversations []adminConversation
	if err := db.Select(&conversations, query); err != nil {
		adminError(c, http.StatusInternalServerError, 20000, "Could not load conversations")
		return
	}
	if conversations == nil {
		conversations = []adminConversation{}
	}
	adminOK(c, conversations)
}

func ListConversationMessages(c *gin.Context) {
	conversationID := c.Param("conversation_uuid")
	db := repo.CurrentDB()

	var count int
	if err := db.Get(&count, db.Rebind("SELECT COUNT(*) FROM conversations WHERE id = ? AND kind = 'group'"), conversationID); err != nil {
		adminError(c, http.StatusInternalServerError, 20000, "Could not load conversation")
		return
	}
	if count == 0 {
		adminError(c, http.StatusNotFound, 20007, "Group conversation not found")
		return
	}

	query := `
		SELECT m.id, m.conversation_id,
		       sender.uuid AS sender_uuid,
		       sender.username AS sender_username,
		       sender.nickname AS sender_nickname,
		       sender.avatar_url AS sender_avatar_url,
		       m.content, m.content_type, m.sequence, m.created_at,
		       m.is_illegal, m.moderated_at
		FROM messages m
		JOIN users sender ON sender.id = m.sender_id
		WHERE m.conversation_id = ?
		ORDER BY m.sequence ASC`
	var messages []adminMessage
	if err := db.Select(&messages, db.Rebind(query), conversationID); err != nil {
		adminError(c, http.StatusInternalServerError, 20000, "Could not load conversation messages")
		return
	}
	if messages == nil {
		messages = []adminMessage{}
	}
	adminOK(c, messages)
}

func MarkMessageIllegal(c *gin.Context) {
	messageID := c.Param("message_uuid")
	db := repo.CurrentDB()
	message, err := loadAdminMessage(db, messageID)
	if errors.Is(err, sql.ErrNoRows) {
		adminError(c, http.StatusNotFound, 20008, "Message not found")
		return
	}
	if err != nil {
		adminError(c, http.StatusInternalServerError, 20000, "Could not load message")
		return
	}
	if message.IsIllegal {
		adminOK(c, message)
		return
	}

	eventID, err := utils.NewULID()
	if err != nil {
		adminError(c, http.StatusInternalServerError, 20000, "Could not moderate message")
		return
	}
	now := time.Now().UTC()
	err = repo.Tx(db, func(tx *sqlx.Tx) error {
		result, err := tx.Exec(tx.Rebind("UPDATE messages SET is_illegal = ?, moderated_at = ? WHERE id = ? AND is_illegal = ?"), true, now, messageID, false)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return nil
		}
		var targets []string
		if err := tx.Select(&targets, tx.Rebind("SELECT u.uuid FROM conversation_members cm JOIN users u ON u.id = cm.user_id WHERE cm.conversation_id = ? AND cm.left_at IS NULL"), message.ConversationID); err != nil {
			return err
		}
		payload, err := json.Marshal(gin.H{
			"event_id":        eventID,
			"event":           "message.moderated",
			"message_id":      message.ID,
			"conversation_id": message.ConversationID,
			"sequence":        message.Sequence,
			"targets":         gin.H{"user_uuids": targets},
		})
		if err != nil {
			return err
		}
		_, err = tx.Exec(tx.Rebind("INSERT INTO outbox_events (id, topic, aggregate_id, payload, created_at, attempts) VALUES (?, 'message.moderated', ?, ?, ?, 0)"), eventID, message.ID, string(payload), now)
		return err
	})
	if err != nil {
		adminError(c, http.StatusInternalServerError, 20000, "Could not moderate message")
		return
	}

	message.IsIllegal = true
	message.ModeratedAt = &now
	adminOK(c, message)
}

func loadAdminMessage(db *sqlx.DB, messageID string) (*adminMessage, error) {
	query := `
		SELECT m.id, m.conversation_id,
		       sender.uuid AS sender_uuid,
		       sender.username AS sender_username,
		       sender.nickname AS sender_nickname,
		       sender.avatar_url AS sender_avatar_url,
		       m.content, m.content_type, m.sequence, m.created_at,
		       m.is_illegal, m.moderated_at
		FROM messages m
		JOIN users sender ON sender.id = m.sender_id
		JOIN conversations c ON c.id = m.conversation_id AND c.kind = 'group'
		WHERE m.id = ?`
	var message adminMessage
	if err := db.Get(&message, db.Rebind(query), messageID); err != nil {
		return nil, err
	}
	return &message, nil
}

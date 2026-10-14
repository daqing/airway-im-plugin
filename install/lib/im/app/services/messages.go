package services

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/utils"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
)

// ReleaseMessage delivers a message into a conversation: it takes the next
// sequence, inserts the row and queues the message.created fan-out. An
// immediate send and an operator approving a queued message both go through
// here, so a released message is indistinguishable from one that was never
// held for review.
//
// It returns sql.ErrNoRows when the conversation no longer exists.
func ReleaseMessage(tx *sqlx.Tx, conversationID, messageID string, senderID int64, content, contentType string, now time.Time) (int64, error) {
	result, err := tx.Exec(tx.Rebind("UPDATE conversations SET next_sequence = next_sequence + 1, updated_at = ? WHERE id = ?"), now, conversationID)
	if err != nil {
		return 0, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return 0, sql.ErrNoRows
	}
	var sequence int64
	if err := tx.Get(&sequence, tx.Rebind("SELECT next_sequence - 1 FROM conversations WHERE id = ?"), conversationID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(tx.Rebind("INSERT INTO messages (id, conversation_id, sender_id, content, content_type, sequence, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)"), messageID, conversationID, senderID, content, contentType, sequence, now); err != nil {
		return 0, err
	}
	eventID, err := utils.NewULID()
	if err != nil {
		return 0, err
	}
	var targets []string
	if err := tx.Select(&targets, tx.Rebind("SELECT u.uuid FROM conversation_members cm JOIN users u ON u.id = cm.user_id WHERE cm.conversation_id = ? AND cm.left_at IS NULL"), conversationID); err != nil {
		return 0, err
	}
	payload, err := json.Marshal(gin.H{"event_id": eventID, "event": "message.created", "message_id": messageID, "conversation_id": conversationID, "sequence": sequence, "targets": gin.H{"user_uuids": targets}})
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(tx.Rebind("INSERT INTO outbox_events (id, topic, aggregate_id, payload, created_at, attempts) VALUES (?, 'message.created', ?, ?, ?, 0)"), eventID, messageID, string(payload), now)
	if err != nil {
		return 0, err
	}
	return sequence, nil
}

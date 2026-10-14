package admin_api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/moderation"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/repo"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/services"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/utils"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
)

var (
	errReviewNotFound = errors.New("review not found")
	errReviewDecided  = errors.New("review is no longer pending")
)

type adminReview struct {
	ID                string            `db:"id" json:"id"`
	ConversationID    string            `db:"conversation_id" json:"conversation_id"`
	ConversationKind  string            `db:"conversation_kind" json:"conversation_kind"`
	ConversationTitle *string           `db:"conversation_title" json:"conversation_title"`
	SenderUUID        string            `db:"sender_uuid" json:"sender_uuid"`
	SenderUsername    string            `db:"sender_username" json:"sender_username"`
	SenderNickname    *string           `db:"sender_nickname" json:"sender_nickname"`
	Content           string            `db:"content" json:"content"`
	ContentType       string            `db:"content_type" json:"content_type"`
	FlagLabels        moderation.Labels `db:"flag_labels" json:"flag_labels"`
	FlagMatches       moderation.Labels `db:"flag_matches" json:"flag_matches"`
	Status            string            `db:"status" json:"status"`
	MessageID         *string           `db:"message_id" json:"message_id"`
	ReviewedBy        *string           `db:"reviewed_by" json:"reviewed_by"`
	ReviewedAt        *time.Time        `db:"reviewed_at" json:"reviewed_at"`
	CreatedAt         time.Time         `db:"created_at" json:"created_at"`
}

const adminReviewQuery = `
	SELECT r.id, r.conversation_id, c.kind AS conversation_kind, c.title AS conversation_title,
	       sender.uuid AS sender_uuid,
	       sender.username AS sender_username,
	       sender.nickname AS sender_nickname,
	       r.content, r.content_type, r.flag_labels, r.flag_matches, r.status,
	       r.message_id, r.reviewed_by, r.reviewed_at, r.created_at
	FROM message_reviews r
	JOIN conversations c ON c.id = r.conversation_id
	JOIN users sender ON sender.id = r.sender_id`

// ListReviews returns the pre-delivery review queue, newest first. Messages a
// client or the server's own screening flagged are parked here and never
// reach a member until an operator approves them.
func ListReviews(c *gin.Context) {
	status := strings.TrimSpace(c.Query("status"))
	if status == "" {
		status = moderation.ReviewPending
	}
	if status != moderation.ReviewPending && status != moderation.ReviewApproved && status != moderation.ReviewRejected {
		adminError(c, http.StatusBadRequest, 20003, "Invalid review status")
		return
	}

	db := repo.CurrentDB()
	var reviews []adminReview
	query := adminReviewQuery + " WHERE r.status = ? ORDER BY r.created_at DESC, r.id DESC"
	if err := db.Select(&reviews, db.Rebind(query), status); err != nil {
		adminError(c, http.StatusInternalServerError, 20000, "Could not load reviews")
		return
	}
	if reviews == nil {
		reviews = []adminReview{}
	}
	adminOK(c, reviews)
}

// ApproveReview releases a queued message into its conversation: it is
// inserted with the review record's own id, so the sender's "pending" bubble
// becomes the delivered message instead of a duplicate.
func ApproveReview(c *gin.Context) {
	reviewID := c.Param("review_id")
	db := repo.CurrentDB()
	now := time.Now().UTC()

	err := repo.Tx(db, func(tx *sqlx.Tx) error {
		if err := claimReview(tx, reviewID, moderation.ReviewApproved, now); err != nil {
			return err
		}
		var review struct {
			ConversationID string `db:"conversation_id"`
			SenderID       int64  `db:"sender_id"`
			Content        string `db:"content"`
			ContentType    string `db:"content_type"`
		}
		if err := tx.Get(&review, tx.Rebind("SELECT conversation_id, sender_id, content, content_type FROM message_reviews WHERE id = ?"), reviewID); err != nil {
			return err
		}
		_, err := services.ReleaseMessage(tx, review.ConversationID, reviewID, review.SenderID, review.Content, review.ContentType, now)
		if err != nil {
			return err
		}
		_, err = tx.Exec(tx.Rebind("UPDATE message_reviews SET message_id = ? WHERE id = ?"), reviewID, reviewID)
		return err
	})
	if respondReviewFailure(c, err) {
		return
	}
	respondReview(c, db, reviewID, "Could not approve review")
}

// RejectReview discards a queued message and labels its sender, so their
// later messages are queued for review too. The sender is told directly,
// otherwise their client would show the message as pending forever.
func RejectReview(c *gin.Context) {
	reviewID := c.Param("review_id")
	db := repo.CurrentDB()
	now := time.Now().UTC()

	err := repo.Tx(db, func(tx *sqlx.Tx) error {
		if err := claimReview(tx, reviewID, moderation.ReviewRejected, now); err != nil {
			return err
		}
		var review struct {
			ConversationID string `db:"conversation_id"`
			SenderID       int64  `db:"sender_id"`
			SenderUUID     string `db:"sender_uuid"`
		}
		query := "SELECT r.conversation_id, r.sender_id, u.uuid AS sender_uuid FROM message_reviews r JOIN users u ON u.id = r.sender_id WHERE r.id = ?"
		if err := tx.Get(&review, tx.Rebind(query), reviewID); err != nil {
			return err
		}
		if err := moderation.AddUserLabel(tx, review.SenderID, moderation.LabelIllegal); err != nil {
			return err
		}
		eventID, err := utils.NewULID()
		if err != nil {
			return err
		}
		payload, err := json.Marshal(gin.H{
			"event_id":        eventID,
			"event":           "message.review_rejected",
			"message_id":      reviewID,
			"conversation_id": review.ConversationID,
			"targets":         gin.H{"user_uuids": []string{review.SenderUUID}},
		})
		if err != nil {
			return err
		}
		_, err = tx.Exec(tx.Rebind("INSERT INTO outbox_events (id, topic, aggregate_id, payload, created_at, attempts) VALUES (?, 'message.review_rejected', ?, ?, ?, 0)"), eventID, reviewID, string(payload), now)
		return err
	})
	if respondReviewFailure(c, err) {
		return
	}
	respondReview(c, db, reviewID, "Could not reject review")
}

// claimReview moves a record out of pending exactly once, so two operators
// acting on the same entry cannot both deliver (or both discard) it.
func claimReview(tx *sqlx.Tx, reviewID, status string, now time.Time) error {
	result, err := tx.Exec(tx.Rebind("UPDATE message_reviews SET status = ?, reviewed_by = ?, reviewed_at = ? WHERE id = ? AND status = ?"), status, currentAdminUsername(), now, reviewID, moderation.ReviewPending)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		var existing string
		if err := tx.Get(&existing, tx.Rebind("SELECT status FROM message_reviews WHERE id = ?"), reviewID); errors.Is(err, sql.ErrNoRows) {
			return errReviewNotFound
		} else if err != nil {
			return err
		}
		return errReviewDecided
	}
	return nil
}

// respondReviewFailure maps the two claim failures onto HTTP; it reports
// whether the request already got a response.
func respondReviewFailure(c *gin.Context, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, errReviewNotFound):
		adminError(c, http.StatusNotFound, 20009, "Review not found")
	case errors.Is(err, errReviewDecided):
		adminError(c, http.StatusConflict, 20010, "Review was already decided")
	default:
		adminError(c, http.StatusInternalServerError, 20000, "Could not update review")
	}
	return true
}

func respondReview(c *gin.Context, db *sqlx.DB, reviewID, failure string) {
	review, err := loadAdminReview(db, reviewID)
	if err != nil {
		adminError(c, http.StatusInternalServerError, 20000, failure)
		return
	}
	adminOK(c, review)
}

func loadAdminReview(db *sqlx.DB, reviewID string) (*adminReview, error) {
	var review adminReview
	if err := db.Get(&review, db.Rebind(adminReviewQuery+" WHERE r.id = ?"), reviewID); err != nil {
		return nil, err
	}
	return &review, nil
}

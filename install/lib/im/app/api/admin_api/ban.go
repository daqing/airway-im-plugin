package admin_api

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/auth"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/moderation"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/repo"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
)

// BanReviewSender permanently bans the sender of a queued message: the review
// is discarded (when still pending), the account is labeled banned so every
// later authentication fails, its credentials are revoked, and its live
// gateway connections drop immediately. Banning cannot be undone from the
// console.
func BanReviewSender(c *gin.Context) {
	reviewID := c.Param("review_id")
	db := repo.CurrentDB()
	now := time.Now().UTC()

	var sender struct {
		ID   int64  `db:"id"`
		UUID string `db:"uuid"`
	}
	err := repo.Tx(db, func(tx *sqlx.Tx) error {
		if err := claimReview(tx, reviewID, moderation.ReviewRejected, now); err != nil {
			// An already-decided entry still lets the operator ban its
			// sender; only a missing entry stops the flow.
			if errors.Is(err, errReviewNotFound) {
				return err
			}
		}
		query := "SELECT u.id, u.uuid FROM message_reviews r JOIN users u ON u.id = r.sender_id WHERE r.id = ?"
		if err := tx.Get(&sender, tx.Rebind(query), reviewID); err != nil {
			return err
		}
		return moderation.AddUserLabel(tx, sender.ID, moderation.LabelBanned)
	})
	switch {
	case errors.Is(err, errReviewNotFound), errors.Is(err, sql.ErrNoRows):
		adminError(c, http.StatusNotFound, 20009, "Review not found")
		return
	case err != nil:
		adminError(c, http.StatusInternalServerError, 20000, "Could not ban user")
		return
	}

	if _, _, err := auth.RevokeUser(sender.UUID); err != nil {
		adminError(c, http.StatusInternalServerError, 20000, "Could not revoke credentials")
		return
	}
	kicked := kickGatewayConnections(sender.UUID)

	review, err := loadAdminReview(db, reviewID)
	if err != nil {
		adminError(c, http.StatusInternalServerError, 20000, "Could not load review")
		return
	}
	adminOK(c, gin.H{
		"review":             review,
		"banned_user_uuid":   sender.UUID,
		"connections_kicked": kicked,
	})
}

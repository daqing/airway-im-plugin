package im_api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/daqing/airway-im-plugin/install/deps/im/app/repo"
	"github.com/daqing/airway-im-plugin/install/deps/im/app/utils"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
)

type addMembersRequest struct {
	MemberUUIDs []string `json:"member_uuids"`
}

// AddMembers adds users to a group conversation. Only an active owner or
// admin may add members. Already-active members are skipped (the operation is
// idempotent); a former member rejoins with left_at cleared, joined_at
// refreshed, and the base "member" role. A conversation.member_added outbox
// event fans out to every active member, including the ones just added.
func AddMembers(c *gin.Context) {
	user, ok := currentUser(c)
	if !ok {
		return
	}

	conversationUUID := strings.TrimSpace(c.Param("conversation_uuid"))
	if len(conversationUUID) != 26 {
		respondError(c, http.StatusBadRequest, 10003, "Invalid conversation UUID")
		return
	}

	var request addMembersRequest
	if err := decodeJSONBody(c.Request.Body, &request); err != nil {
		respondError(c, http.StatusBadRequest, 10003, "Invalid JSON body")
		return
	}

	memberUUIDs := uniqueUUIDs(request.MemberUUIDs)
	delete(memberUUIDs, user.UUID)
	if len(memberUUIDs) == 0 {
		respondError(c, http.StatusBadRequest, 10003, "No members to add")
		return
	}

	db := repo.CurrentDB()
	membership, found, err := loadCallerMembership(db, conversationUUID, user.ID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not load conversation")
		return
	}
	if !found {
		respondError(c, http.StatusNotFound, 11001, "Conversation not found")
		return
	}
	if membership.Kind != "group" {
		respondError(c, http.StatusBadRequest, 10003, "Members can only be added to group conversations")
		return
	}
	if membership.Role != "owner" && membership.Role != "admin" {
		respondError(c, http.StatusForbidden, 10005, "Permission denied")
		return
	}

	resolved, err := resolveUsers(db, memberUUIDs)
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not load members")
		return
	}
	if len(resolved) != len(memberUUIDs) {
		respondError(c, http.StatusBadRequest, 10003, "One or more members do not exist")
		return
	}
	memberIDs := make([]int64, 0, len(resolved))
	for _, member := range resolved {
		memberIDs = append(memberIDs, member.ID)
	}

	eventID, err := utils.NewULID()
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not add members")
		return
	}
	now := time.Now().UTC()
	added := make([]int64, 0, len(memberIDs))
	var targets []int64
	err = repo.Tx(db, func(tx *sqlx.Tx) error {
		for _, memberID := range memberIDs {
			var leftAt *time.Time
			lookupErr := tx.Get(&leftAt, tx.Rebind("SELECT left_at FROM conversation_members WHERE conversation_id = ? AND user_id = ?"), conversationUUID, memberID)
			switch {
			case errors.Is(lookupErr, sql.ErrNoRows):
				if _, err := tx.Exec(tx.Rebind("INSERT INTO conversation_members (conversation_id, user_id, role, joined_at) VALUES (?, ?, 'member', ?)"), conversationUUID, memberID, now); err != nil {
					return err
				}
				added = append(added, memberID)
			case lookupErr != nil:
				return lookupErr
			case leftAt != nil:
				if _, err := tx.Exec(tx.Rebind("UPDATE conversation_members SET left_at = NULL, joined_at = ?, role = 'member' WHERE conversation_id = ? AND user_id = ?"), now, conversationUUID, memberID); err != nil {
					return err
				}
				added = append(added, memberID)
			}
		}
		if len(added) == 0 {
			return nil
		}
		if _, err := tx.Exec(tx.Rebind("UPDATE conversations SET updated_at = ? WHERE id = ?"), now, conversationUUID); err != nil {
			return err
		}
		if err := tx.Select(&targets, tx.Rebind("SELECT user_id FROM conversation_members WHERE conversation_id = ? AND left_at IS NULL"), conversationUUID); err != nil {
			return err
		}
		payload, err := json.Marshal(gin.H{"event_id": eventID, "event": "conversation.member_added", "conversation_id": conversationUUID, "added_user_ids": added, "targets": gin.H{"user_ids": targets}})
		if err != nil {
			return err
		}
		_, err = tx.Exec(tx.Rebind("INSERT INTO outbox_events (id, topic, aggregate_id, payload, created_at, attempts) VALUES (?, 'conversation.member_added', ?, ?, ?, 0)"), eventID, conversationUUID, string(payload), now)
		return err
	})
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not add members")
		return
	}

	memberList, err := loadActiveMembers(db, conversationUUID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not load conversation members")
		return
	}
	respond(c, http.StatusOK, conversationDetailsResponse{
		ConversationUUID: conversationUUID,
		Type:             membership.Kind,
		Members:          memberList,
	})
}

func loadActiveMembers(db *sqlx.DB, conversationUUID string) ([]conversationMemberResponse, error) {
	members := make([]conversationMemberResponse, 0)
	query := `
		SELECT u.id, u.uuid, u.username, u.nickname, u.avatar_url, cm.role
		FROM conversation_members cm
		JOIN users u ON u.id = cm.user_id
		WHERE cm.conversation_id = ? AND cm.left_at IS NULL
		ORDER BY
			CASE cm.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END,
			cm.joined_at ASC,
			u.id ASC
	`
	if err := db.Select(&members, db.Rebind(query), conversationUUID); err != nil {
		return nil, err
	}
	return members, nil
}

type callerMembership struct {
	Kind string `db:"kind"`
	Role string `db:"role"`
}

// loadCallerMembership returns the conversation kind and the caller's active
// role; found is false when the conversation does not exist or the caller is
// not an active member.
func loadCallerMembership(db *sqlx.DB, conversationUUID string, userID int64) (callerMembership, bool, error) {
	var membership callerMembership
	query := `
		SELECT c.kind, cm.role
		FROM conversations c
		JOIN conversation_members cm ON cm.conversation_id = c.id
		WHERE c.id = ? AND cm.user_id = ? AND cm.left_at IS NULL
	`
	err := db.Get(&membership, db.Rebind(query), conversationUUID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return membership, false, nil
	}
	return membership, err == nil, err
}

// RemoveMember removes a user from a group conversation by setting left_at
// (membership history is preserved). Only an active owner or admin may remove
// members, and an admin may only remove plain members. The owner can never be
// removed — that requires an ownership transfer. Removing a user who is not an
// active member is an idempotent no-op. A conversation.member_removed outbox
// event targets every active member plus the removed user, so their client
// learns it was kicked.
func RemoveMember(c *gin.Context) {
	user, ok := currentUser(c)
	if !ok {
		return
	}

	conversationUUID := strings.TrimSpace(c.Param("conversation_uuid"))
	targetUUID := strings.TrimSpace(c.Param("user_uuid"))
	if len(conversationUUID) != 26 || targetUUID == "" || len(targetUUID) > 64 {
		respondError(c, http.StatusBadRequest, 10003, "Invalid conversation UUID or user UUID")
		return
	}
	if targetUUID == user.UUID {
		respondError(c, http.StatusBadRequest, 10003, "Cannot remove yourself")
		return
	}

	db := repo.CurrentDB()
	membership, found, err := loadCallerMembership(db, conversationUUID, user.ID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not load conversation")
		return
	}
	if !found {
		respondError(c, http.StatusNotFound, 11001, "Conversation not found")
		return
	}
	if membership.Kind != "group" {
		respondError(c, http.StatusBadRequest, 10003, "Members can only be removed from group conversations")
		return
	}
	if membership.Role != "owner" && membership.Role != "admin" {
		respondError(c, http.StatusForbidden, 10005, "Permission denied")
		return
	}

	var targetID int64
	resolveErr := db.Get(&targetID, db.Rebind("SELECT id FROM users WHERE uuid = ?"), targetUUID)
	if errors.Is(resolveErr, sql.ErrNoRows) {
		// Unknown user: they can never have been an active member, so the
		// removal is an idempotent no-op.
		memberList, listErr := loadActiveMembers(db, conversationUUID)
		if listErr != nil {
			respondError(c, http.StatusInternalServerError, 10000, "Could not load conversation members")
			return
		}
		respond(c, http.StatusOK, conversationDetailsResponse{
			ConversationUUID: conversationUUID,
			Type:             membership.Kind,
			Members:          memberList,
		})
		return
	}
	if resolveErr != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not load user")
		return
	}

	var targetRole string
	err = db.Get(&targetRole, db.Rebind("SELECT role FROM conversation_members WHERE conversation_id = ? AND user_id = ? AND left_at IS NULL"), conversationUUID, targetID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		respondError(c, http.StatusInternalServerError, 10000, "Could not load member")
		return
	}
	if err == nil {
		if targetRole == "owner" {
			respondError(c, http.StatusBadRequest, 10003, "Cannot remove the group owner")
			return
		}
		if targetRole == "admin" && membership.Role != "owner" {
			respondError(c, http.StatusForbidden, 10005, "Only the owner can remove an admin")
			return
		}
	}

	removed := err == nil // an active member row exists
	if removed {
		eventID, idErr := utils.NewULID()
		if idErr != nil {
			respondError(c, http.StatusInternalServerError, 10000, "Could not remove member")
			return
		}
		now := time.Now().UTC()
		err = repo.Tx(db, func(tx *sqlx.Tx) error {
			if _, err := tx.Exec(tx.Rebind("UPDATE conversation_members SET left_at = ? WHERE conversation_id = ? AND user_id = ? AND left_at IS NULL"), now, conversationUUID, targetID); err != nil {
				return err
			}
			if _, err := tx.Exec(tx.Rebind("UPDATE conversations SET updated_at = ? WHERE id = ?"), now, conversationUUID); err != nil {
				return err
			}
			var targets []int64
			if err := tx.Select(&targets, tx.Rebind("SELECT user_id FROM conversation_members WHERE conversation_id = ? AND left_at IS NULL"), conversationUUID); err != nil {
				return err
			}
			targets = append(targets, targetID)
			payload, err := json.Marshal(gin.H{"event_id": eventID, "event": "conversation.member_removed", "conversation_id": conversationUUID, "removed_user_id": targetID, "targets": gin.H{"user_ids": targets}})
			if err != nil {
				return err
			}
			_, err = tx.Exec(tx.Rebind("INSERT INTO outbox_events (id, topic, aggregate_id, payload, created_at, attempts) VALUES (?, 'conversation.member_removed', ?, ?, ?, 0)"), eventID, conversationUUID, string(payload), now)
			return err
		})
		if err != nil {
			respondError(c, http.StatusInternalServerError, 10000, "Could not remove member")
			return
		}
	}

	memberList, err := loadActiveMembers(db, conversationUUID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not load conversation members")
		return
	}
	respond(c, http.StatusOK, conversationDetailsResponse{
		ConversationUUID: conversationUUID,
		Type:             membership.Kind,
		Members:          memberList,
	})
}

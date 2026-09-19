package im_api

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/daqing/airway-im-plugin/install/deps/im/app/repo"
	"github.com/daqing/airway-im-plugin/install/deps/im/app/utils"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
)

type createConversationRequest struct {
	Kind      string  `json:"kind"`
	Title     *string `json:"title"`
	MemberIDs []int64 `json:"member_ids"`
}

type createGroupRequest struct {
	Title     *string `json:"title"`
	MemberIDs []int64 `json:"member_ids"`
}

type conversationResponse struct {
	ID        string    `db:"id" json:"id"`
	Kind      string    `db:"kind" json:"kind"`
	Title     *string   `db:"title" json:"title"`
	AvatarURL *string   `db:"avatar_url" json:"avatar_url"`
	CreatedBy int64     `db:"created_by" json:"created_by"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

type conversationMemberResponse struct {
	ID        int64   `db:"id" json:"id"`
	Username  string  `db:"username" json:"username"`
	Nickname  *string `db:"nickname" json:"nickname"`
	AvatarURL *string `db:"avatar_url" json:"avatar_url"`
	Role      string  `db:"role" json:"role"`
}

type conversationDetailsResponse struct {
	ConversationUUID string                       `json:"conversation_uuid"`
	Type             string                       `json:"type"`
	Members          []conversationMemberResponse `json:"members"`
}

func GetConversation(c *gin.Context) {
	user, ok := currentUser(c)
	if !ok {
		return
	}

	conversationUUID := strings.TrimSpace(c.Param("conversation_uuid"))
	if len(conversationUUID) != 26 {
		respondError(c, http.StatusBadRequest, 10003, "Invalid conversation UUID")
		return
	}

	db := repo.CurrentDB()
	var conversationType string
	query := `
		SELECT c.kind
		FROM conversations c
		JOIN conversation_members cm ON cm.conversation_id = c.id
		WHERE c.id = ? AND cm.user_id = ? AND cm.left_at IS NULL
	`
	if err := db.Get(&conversationType, db.Rebind(query), conversationUUID, user.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondError(c, http.StatusNotFound, 11001, "Conversation not found")
			return
		}
		respondError(c, http.StatusInternalServerError, 10000, "Could not load conversation")
		return
	}

	members, err := loadActiveMembers(db, conversationUUID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not load conversation members")
		return
	}

	respond(c, http.StatusOK, conversationDetailsResponse{
		ConversationUUID: conversationUUID,
		Type:             conversationType,
		Members:          members,
	})
}

func ListConversations(c *gin.Context) {
	user, ok := currentUser(c)
	if !ok {
		return
	}

	conversationType := strings.ToLower(strings.TrimSpace(c.Query("type")))
	if conversationType != "group" {
		respondError(c, http.StatusBadRequest, 10003, "Conversation type must be group")
		return
	}

	db := repo.CurrentDB()
	query := `
		SELECT c.id, c.kind, c.title, c.avatar_url, c.created_by, c.created_at, c.updated_at
		FROM conversations c
		JOIN conversation_members cm ON cm.conversation_id = c.id
		WHERE cm.user_id = ? AND cm.left_at IS NULL AND c.kind = ?
		ORDER BY c.updated_at DESC, c.id DESC
	`
	conversations := make([]conversationResponse, 0)
	if err := db.Select(&conversations, db.Rebind(query), user.ID, conversationType); err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not load conversations")
		return
	}
	respond(c, http.StatusOK, conversations)
}

func CreateConversation(c *gin.Context) {
	user, ok := currentUser(c)
	if !ok {
		return
	}
	var request createConversationRequest
	if err := decodeJSONBody(c.Request.Body, &request); err != nil {
		respondError(c, http.StatusBadRequest, 10003, "Invalid JSON body")
		return
	}
	createConversation(c, user.ID, request)
}

// CreateGroup creates a group conversation owned by the authenticated user.
func CreateGroup(c *gin.Context) {
	user, ok := currentUser(c)
	if !ok {
		return
	}
	var request createGroupRequest
	if err := decodeJSONBody(c.Request.Body, &request); err != nil {
		respondError(c, http.StatusBadRequest, 10003, "Invalid JSON body")
		return
	}
	createConversation(c, user.ID, createConversationRequest{
		Kind:      "group",
		Title:     request.Title,
		MemberIDs: request.MemberIDs,
	})
}

func createConversation(c *gin.Context, userID int64, request createConversationRequest) {
	request.Kind = strings.ToLower(strings.TrimSpace(request.Kind))
	if request.Kind != "direct" && request.Kind != "group" {
		respondError(c, http.StatusBadRequest, 10003, "Conversation kind must be direct or group")
		return
	}

	members := uniquePositiveIDs(request.MemberIDs)
	delete(members, userID)
	if request.Kind == "direct" && len(members) != 1 {
		respondError(c, http.StatusBadRequest, 10003, "A direct conversation requires one other member")
		return
	}
	if request.Kind == "group" && len(members) == 0 {
		respondError(c, http.StatusBadRequest, 10003, "A group requires at least one other member")
		return
	}

	db := repo.CurrentDB()
	memberIDs := make([]int64, 0, len(members))
	for memberID := range members {
		memberIDs = append(memberIDs, memberID)
	}
	query, args, err := sqlx.In("SELECT COUNT(*) FROM users WHERE id IN (?)", memberIDs)
	if err != nil {
		respondError(c, http.StatusBadRequest, 10003, "Invalid members")
		return
	}
	var existingMembers int
	if err := db.Get(&existingMembers, db.Rebind(query), args...); err != nil || existingMembers != len(memberIDs) {
		respondError(c, http.StatusBadRequest, 10003, "One or more members do not exist")
		return
	}
	if request.Kind == "direct" {
		for target := range members {
			if existing, err := findDirect(db, userID, target); err == nil && existing != nil {
				respond(c, http.StatusOK, existing)
				return
			}
		}
	}

	id, err := utils.NewULID()
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not create conversation")
		return
	}
	now := time.Now().UTC()
	err = repo.Tx(db, func(tx *sqlx.Tx) error {
		if _, err := tx.Exec(tx.Rebind("INSERT INTO conversations (id, kind, title, avatar_url, created_by, next_sequence, created_at, updated_at) VALUES (?, ?, ?, NULL, ?, 1, ?, ?)"), id, request.Kind, request.Title, userID, now, now); err != nil {
			return err
		}
		role := "owner"
		if request.Kind == "direct" {
			role = "member"
		}
		if _, err := tx.Exec(tx.Rebind("INSERT INTO conversation_members (conversation_id, user_id, role, joined_at) VALUES (?, ?, ?, ?)"), id, userID, role, now); err != nil {
			return err
		}
		for memberID := range members {
			if _, err := tx.Exec(tx.Rebind("INSERT INTO conversation_members (conversation_id, user_id, role, joined_at) SELECT ?, id, 'member', ? FROM users WHERE id = ?"), id, now, memberID); err != nil {
				return err
			}
		}
		if request.Kind == "direct" {
			for target := range members {
				low, high := orderedPair(userID, target)
				_, err := tx.Exec(tx.Rebind("INSERT INTO direct_conversations (conversation_id, user_id_low, user_id_high) VALUES (?, ?, ?)"), id, low, high)
				return err
			}
		}
		return nil
	})
	if err != nil {
		if request.Kind == "direct" {
			for target := range members {
				if existing, lookupErr := findDirect(db, userID, target); lookupErr == nil && existing != nil {
					respond(c, http.StatusOK, existing)
					return
				}
			}
		}
		respondError(c, http.StatusInternalServerError, 10000, "Could not create conversation")
		return
	}
	respond(c, http.StatusCreated, conversationResponse{ID: id, Kind: request.Kind, Title: request.Title, CreatedBy: userID, CreatedAt: now, UpdatedAt: now})
}

func findDirect(db *sqlx.DB, first, second int64) (*conversationResponse, error) {
	low, high := orderedPair(first, second)
	query := "SELECT c.id, c.kind, c.title, c.avatar_url, c.created_by, c.created_at, c.updated_at FROM conversations c JOIN direct_conversations d ON d.conversation_id = c.id WHERE d.user_id_low = ? AND d.user_id_high = ?"
	rows, err := db.Queryx(db.Rebind(query), low, high)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	var response conversationResponse
	return &response, rows.StructScan(&response)
}

func uniquePositiveIDs(ids []int64) map[int64]struct{} {
	result := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id > 0 {
			result[id] = struct{}{}
		}
	}
	return result
}

func orderedPair(a, b int64) (int64, int64) {
	values := []int64{a, b}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[0], values[1]
}

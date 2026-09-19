package im_api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/auth"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/repo"
)

func memberCredential(t *testing.T, id int64, username string) string {
	t.Helper()
	credential, err := auth.MintCredential(groupTestSecret, fmt.Sprintf("uuid-%s", username), username, "", "", time.Hour)
	if err != nil {
		t.Fatalf("mint credential: %v", err)
	}
	return credential
}

// seedGroup creates a group owned by user 1 with user 2 as member and user 3
// as a former member who left. User 4 exists but was never a member.
func seedGroup(t *testing.T) string {
	t.Helper()
	db := repo.CurrentDB()
	conversationUUID := "01J2Q7D4N5R8TK6VD3SZ1H0Y9M"
	statements := []string{
		`INSERT INTO conversations (id, kind, title, created_by, next_sequence, created_at, updated_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 'group', 'Backend Team', 1, 1, '2026-07-24 00:00:00', '2026-07-24 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 1, 'owner', '2026-07-24 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 2, 'member', '2026-07-24 00:00:00')`,
		`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at, left_at)
		 VALUES ('01J2Q7D4N5R8TK6VD3SZ1H0Y9M', 3, 'member', '2026-07-24 00:00:00', '2026-07-25 00:00:00')`,
		`INSERT INTO users (id, uuid, username) VALUES (4, 'uuid-carol', 'carol')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed group: %v", err)
		}
	}
	return conversationUUID
}

func postAddMembers(t *testing.T, router http.Handler, credential, conversationUUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/conversations/"+conversationUUID+"/members", strings.NewReader(body))
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestAddMembersByOwner(t *testing.T) {
	router := setupGroupTestRouter(t)
	conversationUUID := seedGroup(t)

	response := postAddMembers(t, router, groupOwnerCredential(t), conversationUUID, `{"member_uuids":["uuid-carol","uuid-carol","uuid-owner",""]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Code int                         `json:"code"`
		Data conversationDetailsResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 0 || len(body.Data.Members) != 3 {
		t.Fatalf("unexpected members: %#v", body.Data.Members)
	}
	newest := body.Data.Members[len(body.Data.Members)-1]
	if newest.ID != 4 || newest.UUID != "uuid-carol" || newest.Username != "carol" || newest.Role != "member" {
		t.Fatalf("unexpected new member: %#v", newest)
	}

	db := repo.CurrentDB()
	var activeCount int
	if err := db.Get(&activeCount, "SELECT COUNT(*) FROM conversation_members WHERE conversation_id = ? AND left_at IS NULL", conversationUUID); err != nil {
		t.Fatalf("count members: %v", err)
	}
	if activeCount != 3 {
		t.Fatalf("active members = %d", activeCount)
	}

	var eventCount int
	if err := db.Get(&eventCount, "SELECT COUNT(*) FROM outbox_events WHERE topic = 'conversation.member_added' AND aggregate_id = ?", conversationUUID); err != nil {
		t.Fatalf("count outbox events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("member_added events = %d", eventCount)
	}
}

func TestAddMembersRejoinsFormerMember(t *testing.T) {
	router := setupGroupTestRouter(t)
	conversationUUID := seedGroup(t)

	response := postAddMembers(t, router, groupOwnerCredential(t), conversationUUID, `{"member_uuids":["uuid-bob"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	db := repo.CurrentDB()
	var rejoined struct {
		Role     string  `db:"role"`
		LeftAt   *string `db:"left_at"`
		RowCount int     `db:"row_count"`
	}
	if err := db.Get(&rejoined.RowCount, "SELECT COUNT(*) FROM conversation_members WHERE conversation_id = ? AND user_id = 3", conversationUUID); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if err := db.Get(&rejoined, "SELECT role, left_at FROM conversation_members WHERE conversation_id = ? AND user_id = 3", conversationUUID); err != nil {
		t.Fatalf("load member: %v", err)
	}
	if rejoined.RowCount != 1 || rejoined.LeftAt != nil || rejoined.Role != "member" {
		t.Fatalf("unexpected rejoin state: %#v", rejoined)
	}
}

func TestAddMembersSkipsActiveMembersWithoutEvent(t *testing.T) {
	router := setupGroupTestRouter(t)
	conversationUUID := seedGroup(t)

	response := postAddMembers(t, router, groupOwnerCredential(t), conversationUUID, `{"member_uuids":["uuid-alice"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	db := repo.CurrentDB()
	var rowCount, eventCount int
	if err := db.Get(&rowCount, "SELECT COUNT(*) FROM conversation_members WHERE conversation_id = ? AND user_id = 2", conversationUUID); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if err := db.Get(&eventCount, "SELECT COUNT(*) FROM outbox_events"); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if rowCount != 1 || eventCount != 0 {
		t.Fatalf("rows = %d, events = %d", rowCount, eventCount)
	}
}

func TestAddMembersAdminAllowedMemberForbidden(t *testing.T) {
	router := setupGroupTestRouter(t)
	conversationUUID := seedGroup(t)
	db := repo.CurrentDB()
	if _, err := db.Exec("UPDATE conversation_members SET role = 'admin' WHERE conversation_id = ? AND user_id = 2", conversationUUID); err != nil {
		t.Fatalf("promote admin: %v", err)
	}

	response := postAddMembers(t, router, memberCredential(t, 2, "alice"), conversationUUID, `{"member_uuids":["uuid-carol"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("admin add: status = %d, body = %s", response.Code, response.Body.String())
	}

	if _, err := db.Exec("UPDATE conversation_members SET role = 'member' WHERE conversation_id = ? AND user_id = 2", conversationUUID); err != nil {
		t.Fatalf("demote member: %v", err)
	}
	response = postAddMembers(t, router, memberCredential(t, 2, "alice"), conversationUUID, `{"member_uuids":["uuid-bob"]}`)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":10005`) {
		t.Fatalf("member add: status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestAddMembersRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(t *testing.T) string
		credential   func(t *testing.T) string
		body         string
		expectedCode int
	}{
		{
			name:         "invalid UUID",
			setup:        func(t *testing.T) string { return "invalid" },
			credential:   func(t *testing.T) string { return groupOwnerCredential(t) },
			body:         `{"member_uuids":["uuid-carol"]}`,
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "unknown conversation",
			setup:        func(t *testing.T) string { return "01J2Q7D4N5R8TK6VD3SZ1H0Y7M" },
			credential:   func(t *testing.T) string { return groupOwnerCredential(t) },
			body:         `{"member_uuids":["uuid-carol"]}`,
			expectedCode: http.StatusNotFound,
		},
		{
			name:         "caller not a member",
			setup:        seedGroup,
			credential:   func(t *testing.T) string { return memberCredential(t, 4, "carol") },
			body:         `{"member_uuids":["uuid-alice"]}`,
			expectedCode: http.StatusNotFound,
		},
		{
			name:         "no members after normalization",
			setup:        seedGroup,
			credential:   func(t *testing.T) string { return groupOwnerCredential(t) },
			body:         `{"member_uuids":["uuid-owner",""]}`,
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "unknown user",
			setup:        seedGroup,
			credential:   func(t *testing.T) string { return groupOwnerCredential(t) },
			body:         `{"member_uuids":["uuid-nobody"]}`,
			expectedCode: http.StatusBadRequest,
		},
		{
			name: "direct conversation",
			setup: func(t *testing.T) string {
				db := repo.CurrentDB()
				if _, err := db.Exec(`INSERT INTO conversations (id, kind, created_by, next_sequence, created_at, updated_at)
					VALUES ('01DIRECT0000000000000000001', 'direct', 1, 1, '2026-07-24 00:00:00', '2026-07-24 00:00:00')`); err != nil {
					t.Fatalf("seed direct: %v", err)
				}
				if _, err := db.Exec(`INSERT INTO conversation_members (conversation_id, user_id, role, joined_at)
					VALUES ('01DIRECT0000000000000000001', 1, 'member', '2026-07-24 00:00:00')`); err != nil {
					t.Fatalf("seed direct member: %v", err)
				}
				return "01DIRECT0000000000000000001"
			},
			credential:   func(t *testing.T) string { return groupOwnerCredential(t) },
			body:         `{"member_uuids":["uuid-carol"]}`,
			expectedCode: http.StatusBadRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := setupGroupTestRouter(t)
			conversationUUID := test.setup(t)
			response := postAddMembers(t, router, test.credential(t), conversationUUID, test.body)
			if response.Code != test.expectedCode {
				t.Fatalf("status = %d (want %d), body = %s", response.Code, test.expectedCode, response.Body.String())
			}
		})
	}
}

func TestAddMembersRequiresAuthentication(t *testing.T) {
	router := setupGroupTestRouter(t)
	conversationUUID := seedGroup(t)
	response := postAddMembers(t, router, "", conversationUUID, `{"member_uuids":["uuid-carol"]}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":10001`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func deleteMember(t *testing.T, router http.Handler, credential, conversationUUID, userUUID string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/conversations/"+conversationUUID+"/members/"+userUUID, nil)
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestRemoveMemberByOwner(t *testing.T) {
	router := setupGroupTestRouter(t)
	conversationUUID := seedGroup(t)

	response := deleteMember(t, router, groupOwnerCredential(t), conversationUUID, "uuid-alice")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Code int                         `json:"code"`
		Data conversationDetailsResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 0 || len(body.Data.Members) != 1 || body.Data.Members[0].ID != 1 {
		t.Fatalf("unexpected members: %#v", body.Data.Members)
	}

	db := repo.CurrentDB()
	var leftAt *string
	if err := db.Get(&leftAt, "SELECT left_at FROM conversation_members WHERE conversation_id = ? AND user_id = 2", conversationUUID); err != nil {
		t.Fatalf("load member: %v", err)
	}
	if leftAt == nil {
		t.Fatal("left_at was not set")
	}
	var eventCount int
	if err := db.Get(&eventCount, "SELECT COUNT(*) FROM outbox_events WHERE topic = 'conversation.member_removed' AND aggregate_id = ?", conversationUUID); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("member_removed events = %d", eventCount)
	}
	var payload string
	if err := db.Get(&payload, "SELECT payload FROM outbox_events WHERE topic = 'conversation.member_removed'"); err != nil {
		t.Fatalf("load event payload: %v", err)
	}
	if !strings.Contains(payload, `"removed_user_id":2`) || !strings.Contains(payload, `"user_ids":[1,2]`) {
		t.Fatalf("unexpected payload: %s", payload)
	}
}

func TestRemoveMemberIsIdempotentNoop(t *testing.T) {
	router := setupGroupTestRouter(t)
	conversationUUID := seedGroup(t)

	// Bob already left; carol was never a member. Both removals succeed
	// without state changes or events.
	for _, userUUID := range []string{"uuid-bob", "uuid-carol"} {
		response := deleteMember(t, router, groupOwnerCredential(t), conversationUUID, userUUID)
		if response.Code != http.StatusOK {
			t.Fatalf("remove %s: status = %d, body = %s", userUUID, response.Code, response.Body.String())
		}
	}
	var eventCount int
	if err := repo.CurrentDB().Get(&eventCount, "SELECT COUNT(*) FROM outbox_events"); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("events = %d", eventCount)
	}
}

func TestRemoveMemberRoleRules(t *testing.T) {
	tests := []struct {
		name         string
		caller       string // owner | admin | member
		target       string // uuid of the user to remove
		setup        func(t *testing.T, db_role string)
		expectedCode int
	}{
		{name: "admin removes member", caller: "admin", target: "uuid-carol", expectedCode: http.StatusOK},
		{name: "admin cannot remove admin", caller: "admin", target: "uuid-bob", expectedCode: http.StatusForbidden},
		{name: "member cannot remove", caller: "member", target: "uuid-carol", expectedCode: http.StatusForbidden},
		{name: "owner cannot be removed", caller: "owner", target: "uuid-owner", expectedCode: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := setupGroupTestRouter(t)
			conversationUUID := seedGroup(t)
			db := repo.CurrentDB()
			// user 2 acts as the caller with the requested role; user 3 rejoins
			// as admin for the admin-target case; user 4 joins as a member.
			if _, err := db.Exec("UPDATE conversation_members SET role = ? WHERE conversation_id = ? AND user_id = 2", test.caller, conversationUUID); err != nil {
				t.Fatalf("set caller role: %v", err)
			}
			if _, err := db.Exec("UPDATE conversation_members SET left_at = NULL, role = 'admin' WHERE conversation_id = ? AND user_id = 3", conversationUUID); err != nil {
				t.Fatalf("rejoin user 3: %v", err)
			}
			if _, err := db.Exec("INSERT INTO conversation_members (conversation_id, user_id, role, joined_at) VALUES (?, 4, 'member', '2026-07-26 00:00:00')", conversationUUID); err != nil {
				t.Fatalf("add user 4: %v", err)
			}

			response := deleteMember(t, router, memberCredential(t, 2, "alice"), conversationUUID, test.target)
			if response.Code != test.expectedCode {
				t.Fatalf("status = %d (want %d), body = %s", response.Code, test.expectedCode, response.Body.String())
			}
		})
	}
}

func TestRemoveMemberOwnerRemovesAdmin(t *testing.T) {
	router := setupGroupTestRouter(t)
	conversationUUID := seedGroup(t)
	db := repo.CurrentDB()
	if _, err := db.Exec("UPDATE conversation_members SET left_at = NULL, role = 'admin' WHERE conversation_id = ? AND user_id = 3", conversationUUID); err != nil {
		t.Fatalf("rejoin as admin: %v", err)
	}
	response := deleteMember(t, router, groupOwnerCredential(t), conversationUUID, "uuid-bob")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestRemoveMemberRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name           string
		conversationID string
		userUUID       string
		credential     func(t *testing.T) string
		expectedCode   int
	}{
		{name: "invalid UUID", conversationID: "invalid", userUUID: "uuid-alice", credential: groupOwnerCredential, expectedCode: http.StatusBadRequest},
		{name: "invalid user uuid", conversationID: "01J2Q7D4N5R8TK6VD3SZ1H0Y9M", userUUID: strings.Repeat("x", 65), credential: groupOwnerCredential, expectedCode: http.StatusBadRequest},
		{name: "unknown conversation", conversationID: "01J2Q7D4N5R8TK6VD3SZ1H0Y7M", userUUID: "uuid-alice", credential: groupOwnerCredential, expectedCode: http.StatusNotFound},
		{name: "caller not a member", conversationID: "01J2Q7D4N5R8TK6VD3SZ1H0Y9M", userUUID: "uuid-alice", credential: func(t *testing.T) string { return memberCredential(t, 4, "carol") }, expectedCode: http.StatusNotFound},
		{name: "remove self", conversationID: "01J2Q7D4N5R8TK6VD3SZ1H0Y9M", userUUID: "uuid-owner", credential: groupOwnerCredential, expectedCode: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := setupGroupTestRouter(t)
			seedGroup(t)
			response := deleteMember(t, router, test.credential(t), test.conversationID, test.userUUID)
			if response.Code != test.expectedCode {
				t.Fatalf("status = %d (want %d), body = %s", response.Code, test.expectedCode, response.Body.String())
			}
		})
	}
}

func TestRemoveMemberRequiresAuthentication(t *testing.T) {
	router := setupGroupTestRouter(t)
	conversationUUID := seedGroup(t)
	response := deleteMember(t, router, "", conversationUUID, "uuid-alice")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":10001`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

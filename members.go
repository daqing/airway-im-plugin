package implugin

import (
	"github.com/daqing/airway-im-plugin/install/lib/im/app/api/im_api"
)

// AddGroupMembers adds users to an existing group conversation in-process,
// without a caller-role check. It is the trusted host entry point: the host
// has already authorized the membership change (for example, an invitee
// accepting a group invitation the host recorded) and calls this instead of
// POST /api/v1/conversations/:id/members, which would require an owner/admin
// credential. Being a group is still enforced, membership is idempotent
// (active members are skipped, former members rejoin), and a
// conversation.member_added event fans out to every active member when
// someone actually joined. Requires the framework database to be set up.
func AddGroupMembers(conversationUUID string, memberUUIDs []string) (added []string, err error) {
	return im_api.AddGroupMembersDirect(conversationUUID, memberUUIDs)
}

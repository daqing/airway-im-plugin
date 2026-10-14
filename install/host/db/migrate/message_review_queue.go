package migrate

import "github.com/daqing/airway/lib/migrate/schema"

// RegisterMessageReviewQueue adds the pre-delivery human review queue and the
// user labels an operator can attach to a sender. A flagged message is parked
// in message_reviews instead of messages, so it takes no sequence and no
// member ever sees it until an operator approves it. The review id is the
// message id the message gets on release, which is what lets a client dedupe
// its "pending" bubble against the delivered message.
func RegisterMessageReviewQueue() {
	schema.RegisterChange("20261014000000", "create_message_review_queue", func(m *schema.Migrator) {
		m.AddColumn("users", schema.Column{
			Name:    "labels",
			Type:    schema.Type{Kind: schema.TypeText},
			Null:    schema.Bool(false),
			Default: "[]",
		})
		m.CreateTable("message_reviews", func(t *schema.Table) {
			t.String("id", 26).Null(false).Unique()
			t.String("conversation_id", 26).Null(false)
			t.BigInt("sender_id").Null(false)
			t.Text("content").Null(false)
			t.String("content_type", 32).Null(false)
			t.Text("flag_labels").Null(false).Default("[]")
			t.String("status", 16).Null(false).Default("pending")
			t.String("message_id", 26).Null(true)
			t.String("reviewed_by").Null(true)
			t.DateTime("reviewed_at").Null(true)
			t.DateTime("created_at").Null(false).Default(schema.CurrentTimestamp)
			t.Index("status", "created_at")
		})
	})
}

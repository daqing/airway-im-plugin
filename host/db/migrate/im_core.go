package migrate

import "github.com/daqing/airway/lib/migrate/schema"

// RegisterIMCore creates the durable conversation, message, and outbox tables.
func RegisterIMCore() {
	schema.RegisterChange("20260721000000", "create_im_core", func(m *schema.Migrator) {
		m.CreateTable("conversations", func(t *schema.Table) {
			t.String("id", 26).Null(false).Unique()
			t.String("kind", 16).Null(false)
			t.String("title").Null(true)
			t.String("avatar_url", 2048).Null(true)
			t.BigInt("created_by").Null(false)
			t.BigInt("next_sequence").Null(false).Default(1)
			t.Timestamps()
		})

		m.CreateTable("conversation_members", func(t *schema.Table) {
			t.String("conversation_id", 26).Null(false)
			t.BigInt("user_id").Null(false)
			t.String("role", 16).Null(false).Default("member")
			t.DateTime("joined_at").Null(false).Default(schema.CurrentTimestamp)
			t.DateTime("left_at").Null(true)
			t.UniqueIndex("conversation_id", "user_id")
			t.Index("user_id", "conversation_id")
		})

		m.CreateTable("direct_conversations", func(t *schema.Table) {
			t.String("conversation_id", 26).Null(false).Unique()
			t.BigInt("user_id_low").Null(false)
			t.BigInt("user_id_high").Null(false)
			t.UniqueIndex("user_id_low", "user_id_high")
		})

		m.CreateTable("messages", func(t *schema.Table) {
			t.String("id", 26).Null(false).Unique()
			t.String("conversation_id", 26).Null(false)
			t.BigInt("sender_id").Null(false)
			t.Text("content").Null(false)
			t.String("content_type", 32).Null(false)
			t.BigInt("sequence").Null(false)
			t.DateTime("created_at").Null(false).Default(schema.CurrentTimestamp)
			t.UniqueIndex("conversation_id", "sequence")
			t.Index("conversation_id", "created_at")
		})

		m.CreateTable("message_idempotency", func(t *schema.Table) {
			t.BigInt("user_id").Null(false)
			t.String("idempotency_key", 128).Null(false)
			t.String("request_hash", 64).Null(false)
			t.String("message_id", 26).Null(false)
			t.DateTime("created_at").Null(false).Default(schema.CurrentTimestamp)
			t.UniqueIndex("user_id", "idempotency_key")
		})

		m.CreateTable("outbox_events", func(t *schema.Table) {
			t.String("id", 26).Null(false).Unique()
			t.String("topic", 64).Null(false)
			t.String("aggregate_id", 26).Null(false)
			t.JSON("payload").Null(false)
			t.DateTime("created_at").Null(false).Default(schema.CurrentTimestamp)
			t.DateTime("published_at").Null(true)
			t.Integer("attempts").Null(false).Default(0)
			t.Index("published_at", "created_at")
		})
	})
}

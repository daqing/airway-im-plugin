package migrate

import "github.com/daqing/airway/lib/migrate/schema"

// RegisterMessageModeration adds the durable moderation state for messages.
func RegisterMessageModeration() {
	schema.RegisterChange("20260724000000", "add_message_moderation", func(m *schema.Migrator) {
		m.AddColumn("messages", schema.Column{
			Name:    "is_illegal",
			Type:    schema.Type{Kind: schema.TypeBoolean},
			Null:    schema.Bool(false),
			Default: false,
		})
		m.AddColumn("messages", schema.Column{
			Name: "moderated_at",
			Type: schema.Type{Kind: schema.TypeDateTime},
			Null: schema.Bool(true),
		})
		m.AddIndex("messages", "is_illegal")
	})
}

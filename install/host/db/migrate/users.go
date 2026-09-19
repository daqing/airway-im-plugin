package migrate

import "github.com/daqing/airway/lib/migrate/schema"

// RegisterUsers creates the IM identity table. The plugin ships no login
// flow: users are auto-registered on first authentication, keyed by the
// uuid carried inside a host-signed credential (see deps/im/docs/design/identity.md).
func RegisterUsers() {
	schema.RegisterChange("20260914000000", "create_users", func(m *schema.Migrator) {
		m.CreateTable("users", func(t *schema.Table) {
			t.ID()
			t.String("uuid", 64).Null(false).Unique()
			t.String("username").Null(false)
			t.String("nickname").Null(true)
			t.String("avatar_url", 2048).Null(true)
			t.String("email").Null(true)
			t.DateTime("last_seen_at").Null(true)
			t.Timestamps()
		})
	})
}

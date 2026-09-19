package migrate

import "github.com/daqing/airway/lib/migrate/schema"

// RegisterUserTokenVersion adds the credential revocation counter. Credentials
// minted by the backend carry the user's current token_version; bumping it
// (admin revoke) invalidates every versioned credential at once. See
// deps/im/docs/design/identity.md §9.
func RegisterUserTokenVersion() {
	schema.RegisterChange("20260915000000", "add_user_token_version", func(m *schema.Migrator) {
		m.AddColumn("users", schema.Column{
			Name:    "token_version",
			Type:    schema.Type{Kind: schema.TypeBigInt},
			Null:    schema.Bool(false),
			Default: 1,
		})
	})
}

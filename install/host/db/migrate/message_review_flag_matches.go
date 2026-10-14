package migrate

import "github.com/daqing/airway/lib/migrate/schema"

// RegisterMessageReviewFlagMatches records what the screening service found
// in a queued message: the words that hit, or "screening-unavailable" when
// the service could not answer. Reviewers see why a message was held instead
// of only that it was.
func RegisterMessageReviewFlagMatches() {
	schema.RegisterChange("20261014120000", "add_message_review_flag_matches", func(m *schema.Migrator) {
		m.AddColumn("message_reviews", schema.Column{
			Name:    "flag_matches",
			Type:    schema.Type{Kind: schema.TypeText},
			Null:    schema.Bool(false),
			Default: "[]",
		})
	})
}

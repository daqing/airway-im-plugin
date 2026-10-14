package moderation

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/jmoiron/sqlx"
)

// Labels is a set of short strings stored as JSON array text (users.labels,
// message_reviews.flag_labels, message_reviews.flag_matches). It scans the
// column and marshals back as a real JSON array, so API responses never carry
// a doubly-encoded string.
type Labels []string

func (l *Labels) Scan(value any) error {
	var raw string
	switch v := value.(type) {
	case nil:
	case string:
		raw = v
	case []byte:
		raw = string(v)
	default:
		return errors.New("moderation: labels column is not text")
	}
	parsed, err := ParseLabels(raw)
	if err != nil {
		return err
	}
	*l = parsed
	return nil
}

// ParseLabels decodes label column text, treating empty and NULL values as no
// labels so a row written before the column existed still reads cleanly.
func ParseLabels(raw string) (Labels, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Labels{}, nil
	}
	var labels Labels
	if err := json.Unmarshal([]byte(trimmed), &labels); err != nil {
		return nil, err
	}
	return labels, nil
}

// Has reports whether the set contains label, compared case-insensitively.
func (l Labels) Has(label string) bool {
	for _, existing := range l {
		if strings.EqualFold(existing, label) {
			return true
		}
	}
	return false
}

// With returns the set plus label, unchanged when it is already present.
func (l Labels) With(label string) Labels {
	if l.Has(label) {
		return l
	}
	next := make(Labels, 0, len(l)+1)
	next = append(next, l...)
	return append(next, label)
}

// JSON renders the set as the text form stored in the label columns.
func (l Labels) JSON() (string, error) {
	if l == nil {
		l = Labels{}
	}
	data, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// UserHasLabel reports whether the user carries the label. It reads the
// labels column rather than the caller's cached user row, which is also
// populated on the auto-registration path.
func UserHasLabel(db *sqlx.DB, userID int64, label string) (bool, error) {
	var raw string
	if err := db.Get(&raw, db.Rebind("SELECT labels FROM users WHERE id = ?"), userID); err != nil {
		return false, err
	}
	labels, err := ParseLabels(raw)
	if err != nil {
		return false, err
	}
	return labels.Has(label), nil
}

// AddUserLabel appends label to the user's label set, leaving it untouched
// when it is already there.
func AddUserLabel(tx *sqlx.Tx, userID int64, label string) error {
	var raw string
	if err := tx.Get(&raw, tx.Rebind("SELECT labels FROM users WHERE id = ?"), userID); err != nil {
		return err
	}
	labels, err := ParseLabels(raw)
	if err != nil {
		return err
	}
	next := labels.With(label)
	if len(next) == len(labels) {
		return nil
	}
	encoded, err := next.JSON()
	if err != nil {
		return err
	}
	_, err = tx.Exec(tx.Rebind("UPDATE users SET labels = ? WHERE id = ?"), encoded, userID)
	return err
}

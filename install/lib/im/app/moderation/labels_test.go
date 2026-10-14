package moderation

import (
	"testing"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/repo"
	"github.com/jmoiron/sqlx"
)

func setupLabelDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := repo.SetupDB("sqlite://:memory:")
	if err != nil {
		t.Fatalf("setup database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		uuid VARCHAR(64) NOT NULL UNIQUE,
		labels TEXT NOT NULL DEFAULT '[]'
	)`); err != nil {
		t.Fatalf("create users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, uuid) VALUES (1, 'uuid-one'), (2, 'uuid-two')`); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	return db
}

func TestParseLabelsTreatsBlankAsEmpty(t *testing.T) {
	for _, raw := range []string{"", "  ", "null"} {
		labels, err := ParseLabels(raw)
		if err != nil || len(labels) != 0 {
			t.Fatalf("ParseLabels(%q) = %v, %v", raw, labels, err)
		}
	}
}

func TestLabelsWithDedupesAndJSONRoundTrips(t *testing.T) {
	labels := Labels{}.With(LabelIllegal).With(LabelIllegal)
	if len(labels) != 1 {
		t.Fatalf("labels = %v", labels)
	}
	encoded, err := labels.JSON()
	if err != nil {
		t.Fatalf("encode labels: %v", err)
	}
	if encoded != `["illegal"]` {
		t.Fatalf("encoded = %s", encoded)
	}
	var scanned Labels
	if err := scanned.Scan([]byte(encoded)); err != nil {
		t.Fatalf("scan labels: %v", err)
	}
	if !scanned.Has("ILLEGAL") {
		t.Fatalf("Has is not case-insensitive: %v", scanned)
	}
}

func TestUserLabelRoundTrip(t *testing.T) {
	db := setupLabelDB(t)

	found, err := UserHasLabel(db, 1, LabelIllegal)
	if err != nil || found {
		t.Fatalf("fresh user has label: %v, %v", found, err)
	}

	if err := repo.Tx(db, func(tx *sqlx.Tx) error {
		if err := AddUserLabel(tx, 1, LabelIllegal); err != nil {
			return err
		}
		// Adding the same label twice must not duplicate it.
		return AddUserLabel(tx, 1, LabelIllegal)
	}); err != nil {
		t.Fatalf("add label: %v", err)
	}

	var stored string
	if err := db.Get(&stored, "SELECT labels FROM users WHERE id = 1"); err != nil {
		t.Fatalf("read labels: %v", err)
	}
	if stored != `["illegal"]` {
		t.Fatalf("stored labels = %s", stored)
	}
	if found, err = UserHasLabel(db, 1, LabelIllegal); err != nil || !found {
		t.Fatalf("labeled user: %v, %v", found, err)
	}
	// The label is per user.
	if found, err = UserHasLabel(db, 2, LabelIllegal); err != nil || found {
		t.Fatalf("other user: %v, %v", found, err)
	}
}

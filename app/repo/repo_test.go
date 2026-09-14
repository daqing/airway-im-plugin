package repo

import (
	"errors"
	"testing"

	"github.com/jmoiron/sqlx"
)

func setup(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := SetupDB("sqlite://:memory:")
	if err != nil {
		t.Fatalf("setup database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSetupDBExposesSharedConnection(t *testing.T) {
	db := setup(t)

	current, ok := CurrentDBOK()
	if !ok || current == nil {
		t.Fatal("CurrentDBOK reports no database after SetupDB")
	}
	if CurrentDB() == nil {
		t.Fatal("CurrentDB returned nil after SetupDB")
	}

	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	// The wrapper shares the framework's connection: writes through one
	// handle are visible through the other.
	if _, err := CurrentDB().Exec("INSERT INTO items (name) VALUES ('shared')"); err != nil {
		t.Fatalf("insert via CurrentDB: %v", err)
	}
	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM items"); err != nil || count != 1 {
		t.Fatalf("rows = %d, err = %v", count, err)
	}
}

func TestRebindUsesQuestionMarkPlaceholdersOnSQLite(t *testing.T) {
	db := setup(t)
	const query = "SELECT * FROM items WHERE id = ? AND name = ?"
	if got := db.Rebind(query); got != query {
		t.Fatalf("Rebind rewrote sqlite placeholders: %q", got)
	}
}

func TestTxCommitsOnSuccess(t *testing.T) {
	db := setup(t)
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatal(err)
	}

	err := Tx(db, func(tx *sqlx.Tx) error {
		_, err := tx.Exec("INSERT INTO items (name) VALUES ('committed')")
		return err
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}
	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM items"); err != nil || count != 1 {
		t.Fatalf("rows after commit = %d, err = %v", count, err)
	}
}

func TestTxRollsBackOnError(t *testing.T) {
	db := setup(t)
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("boom")
	err := Tx(db, func(tx *sqlx.Tx) error {
		if _, err := tx.Exec("INSERT INTO items (name) VALUES ('rolled back')"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx error = %v, want %v", err, boom)
	}
	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM items"); err != nil || count != 0 {
		t.Fatalf("rows after rollback = %d, err = %v", count, err)
	}
}

func TestTxRollsBackOnPanic(t *testing.T) {
	db := setup(t)
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatal(err)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Error("Tx did not propagate the panic")
			}
		}()
		_ = Tx(db, func(tx *sqlx.Tx) error {
			if _, err := tx.Exec("INSERT INTO items (name) VALUES ('panicked')"); err != nil {
				return err
			}
			panic("kaput")
		})
	}()

	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM items"); err != nil || count != 0 {
		t.Fatalf("rows after panic = %d, err = %v", count, err)
	}
}

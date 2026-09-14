// Package repo is a thin sqlx facade over the framework's database/sql
// connection. The IM code ported from the reference backend speaks sqlx
// (Get/Select/StructScan/Rebind and transactional helpers); this package
// exposes exactly that API on top of lib/repo, which owns the connection
// pool, driver detection, and DSN normalization.
package repo

import (
	"github.com/daqing/airway/lib/repo"
	"github.com/jmoiron/sqlx"
)

// CurrentDB returns a sqlx wrapper around the framework's global connection.
// The wrapper shares the underlying *sql.DB, so pooling is owned by lib/repo.
func CurrentDB() *sqlx.DB {
	return wrap(repo.CurrentDB())
}

// CurrentDBOK reports whether the database has been set up and returns the
// sqlx wrapper when it has.
func CurrentDBOK() (*sqlx.DB, bool) {
	base, ok := repo.CurrentDBOK()
	if !ok {
		return nil, false
	}
	return wrap(base), true
}

// SetupDB sets up the framework database and returns its sqlx wrapper. It is
// used by tests; the server process calls lib/repo.SetupDB during boot.
func SetupDB(dsn string) (*sqlx.DB, error) {
	base, err := repo.SetupDB(dsn)
	if err != nil {
		return nil, err
	}
	return wrap(base), nil
}

// Tx runs fn inside a transaction, committing on success and rolling back on
// error or panic.
func Tx(db *sqlx.DB, fn func(tx *sqlx.Tx) error) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit()
}

// wrap maps the framework driver onto the sqlx driver names so Rebind picks
// the right placeholder style ($N on Postgres, ? elsewhere).
func wrap(base *repo.DB) *sqlx.DB {
	driver := string(base.Driver())
	switch driver {
	case "pgx":
		driver = "postgres"
	}
	return sqlx.NewDb(base.Conn(), driver)
}

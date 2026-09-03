package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers as "sqlite"
)

// SQLiteStore is the SQLite-backed implementation of Store. It is the only
// place in the module that issues SQL.
type SQLiteStore struct {
	db *sql.DB
}

var _ Store = (*SQLiteStore)(nil)

// Open opens (creating if necessary) a SQLite database at dsn and returns a
// Store backed by it. dsn is passed to modernc.org/sqlite with pragmas
// appended, so both file paths and "file::memory:?cache=shared"-style DSNs
// work; any query string already present in dsn is preserved.
//
// foreign_keys, busy_timeout, journal_mode, and synchronous are applied via
// the DSN's _pragma parameters rather than a one-shot PRAGMA exec after Open:
// foreign_keys in particular is per-connection and defaults OFF, so setting
// it against a single already-open connection only holds as long as
// database/sql never recycles that connection, and WAL's journal_mode must
// be re-declared per connection or it lapses to delete on a new handle. DSN
// pragmas are re-applied by the driver on every new connection it opens, so
// enforcement can't silently lapse. journal_mode=WAL on a memory DSN is a
// no-op (ADR 0030).
func Open(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", withPragmas(dsn))
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", dsn, err)
	}
	// SQLite allows only one writer at a time; serialize through a single
	// connection so concurrent callers wait rather than hit SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	return &SQLiteStore{db: db}, nil
}

func withPragmas(dsn string) string {
	pragmas := "_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	if strings.Contains(dsn, "?") {
		return dsn + "&" + pragmas
	}
	return dsn + "?" + pragmas
}

// Close releases the underlying database connection.
func (s *SQLiteStore) Close() error { return s.db.Close() }

// Migrate brings the schema up to date. Safe to call repeatedly.
func (s *SQLiteStore) Migrate(ctx context.Context) error {
	return migrate(ctx, s.db)
}

// querier is satisfied by both *sql.DB and *sql.Tx, letting read/write
// helpers below run against either a plain connection or an in-flight
// transaction without duplicating their SQL.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// scanner is satisfied by both *sql.Row and *sql.Rows, letting a single
// scan function serve both a single-row lookup and a bulk row-by-row scan.
type scanner interface {
	Scan(dest ...any) error
}

func isUniqueConstraintErr(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

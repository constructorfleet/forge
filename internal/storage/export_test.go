package storage

import (
	"context"
	"database/sql"
	"errors"
)

// TableExists reports whether table is present in the SQLite schema.
// Test-only: lives in _test.go so it compiles out of production builds.
// Used by migration tests to assert every table the ticket requires was
// actually created.
func (s *SQLiteStore) TableExists(ctx context.Context, table string) (bool, error) {
	var name string
	row := s.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table)
	switch err := row.Scan(&name); {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, err
	}
}

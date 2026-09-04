package storage

// schema.go: schema-verification helpers for read-only observers (the TUI's
// watch path and live roster), which must never run Migrate.

import (
	"context"
	"fmt"
)

// LivenessColumnsPresent reports whether the two display-only liveness
// columns (workers.last_heartbeat and execution_issues.state_changed_at,
// migration 0028) exist. The roster renders liveness and elapsed from them,
// so a watch against a database that predates 0028 must fail loudly rather
// than misrender.
func (s *SQLiteStore) LivenessColumnsPresent(ctx context.Context) (bool, error) {
	worker, err := s.columnExists(ctx, "workers", "last_heartbeat")
	if err != nil {
		return false, err
	}
	issue, err := s.columnExists(ctx, "execution_issues", "state_changed_at")
	if err != nil {
		return false, err
	}
	return worker && issue, nil
}

func (s *SQLiteStore) columnExists(ctx context.Context, table, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, fmt.Errorf("storage: inspect schema of %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notNull int
			dflt    any
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return false, fmt.Errorf("storage: read column of %s: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("storage: read columns of %s: %w", table, err)
	}
	return false, nil
}

package storage

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ClaimIssue records a Worker claim on an Issue and appends a claim Event,
// transactionally. Returns ErrAlreadyClaimed if the Issue is already
// claimed within the Execution (workers.UNIQUE(execution_id, issue_id)) or
// ErrNotFound if the Issue doesn't exist
// (workers.FOREIGN KEY -> execution_issues), in both cases translating a
// database constraint violation rather than doing a read-then-write check
// that would race.
func (s *SQLiteStore) ClaimIssue(ctx context.Context, executionID, issueID, workerRef string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: claim issue %s/%s: %w", executionID, issueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workers (execution_id, issue_id, worker_ref, claimed_at)
		VALUES (?, ?, ?, ?)`,
		executionID, issueID, workerRef, now,
	); err != nil {
		switch {
		case isUniqueConstraintErr(err):
			return fmt.Errorf("storage: claim issue %s/%s: %w", executionID, issueID, ErrAlreadyClaimed)
		case isForeignKeyConstraintErr(err):
			return fmt.Errorf("storage: claim issue %s/%s: %w", executionID, issueID, ErrNotFound)
		default:
			return fmt.Errorf("storage: claim issue %s/%s: %w", executionID, issueID, err)
		}
	}

	if err := appendClaimEvent(ctx, tx, executionID, issueID, workerRef, now); err != nil {
		return fmt.Errorf("storage: claim issue %s/%s: %w", executionID, issueID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: claim issue %s/%s: %w", executionID, issueID, err)
	}
	return nil
}

func isForeignKeyConstraintErr(err error) bool {
	return strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}

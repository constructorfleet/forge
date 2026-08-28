package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ClaimIssue records a Worker claim on an Issue and appends a claim Event,
// transactionally. Returns ErrAlreadyClaimed if the Issue is already
// claimed by any active Execution (workers.UNIQUE(issue_id)) or ErrNotFound
// if the Issue doesn't exist (workers.FOREIGN KEY -> execution_issues), in
// both cases translating a database constraint violation rather than doing
// a read-then-write check that would race.
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
			claim, loadErr := activeClaimByIssue(ctx, tx, issueID)
			if loadErr != nil {
				return fmt.Errorf("storage: claim issue %s/%s: %w", executionID, issueID, ErrAlreadyClaimed)
			}
			return fmt.Errorf("storage: claim issue %s/%s: %w", executionID, issueID, &ClaimConflictError{
				IssueID:           issueID,
				OwningExecutionID: claim.ExecutionID,
				OwningWorkerRef:   claim.WorkerRef,
			})
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

// UpdateWorkerOwner records the owning process ID for an active Worker
// claim.
func (s *SQLiteStore) UpdateWorkerOwner(ctx context.Context, executionID, issueID string, ownerPID int) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE workers
		SET owner_pid = ?
		WHERE execution_id = ? AND issue_id = ?`,
		ownerPID, executionID, issueID,
	)
	if err != nil {
		return fmt.Errorf("storage: update worker owner for issue %s/%s: %w", executionID, issueID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: update worker owner for issue %s/%s: %w", executionID, issueID, err)
	}
	if affected == 0 {
		return fmt.Errorf("storage: update worker owner for issue %s/%s: %w", executionID, issueID, ErrNotFound)
	}
	return nil
}

// WorkerClaim reloads the active Worker claim for one Issue.
func (s *SQLiteStore) WorkerClaim(ctx context.Context, executionID, issueID string) (WorkerClaim, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT execution_id, issue_id, worker_ref, owner_pid, claimed_at
		FROM workers
		WHERE execution_id = ? AND issue_id = ?`,
		executionID, issueID,
	)
	var claim WorkerClaim
	if err := row.Scan(&claim.ExecutionID, &claim.IssueID, &claim.WorkerRef, &claim.OwnerPID, &claim.ClaimedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkerClaim{}, fmt.Errorf("storage: worker claim %s/%s: %w", executionID, issueID, ErrNotFound)
		}
		return WorkerClaim{}, fmt.Errorf("storage: load worker claim %s/%s: %w", executionID, issueID, err)
	}
	claim.ClaimedAt = claim.ClaimedAt.UTC()
	return claim, nil
}

func activeClaimByIssue(ctx context.Context, q querier, issueID string) (WorkerClaim, error) {
	row := q.QueryRowContext(ctx, `
		SELECT execution_id, issue_id, worker_ref, owner_pid, claimed_at
		FROM workers
		WHERE issue_id = ?`,
		issueID,
	)
	var claim WorkerClaim
	if err := row.Scan(&claim.ExecutionID, &claim.IssueID, &claim.WorkerRef, &claim.OwnerPID, &claim.ClaimedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkerClaim{}, fmt.Errorf("storage: worker claim for issue %s: %w", issueID, ErrNotFound)
		}
		return WorkerClaim{}, fmt.Errorf("storage: load worker claim for issue %s: %w", issueID, err)
	}
	claim.ClaimedAt = claim.ClaimedAt.UTC()
	return claim, nil
}

// ReleaseWorkerClaim removes the active Worker claim for one Issue.
func (s *SQLiteStore) ReleaseWorkerClaim(ctx context.Context, executionID, issueID string) error {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM workers
		WHERE execution_id = ? AND issue_id = ?`,
		executionID, issueID,
	); err != nil {
		return fmt.Errorf("storage: release worker claim %s/%s: %w", executionID, issueID, err)
	}
	return nil
}

func isForeignKeyConstraintErr(err error) bool {
	return strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}

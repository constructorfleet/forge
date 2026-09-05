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
// claimed by any active Execution, or ErrNotFound if the Issue doesn't
// exist (workers.FOREIGN KEY -> execution_issues). In both cases it
// translates a database constraint violation rather than doing a
// read-then-write check that would race.
//
// The global guarantee is the unique index on workers.issue_id from
// migration 0011. Migration 0031 dropped the weaker table-level
// UNIQUE(execution_id, issue_id) from 0001_init.sql, which the index made
// redundant. activeClaimByIssue relies on the index: one Issue has at most
// one active claim across all Executions.
func (s *SQLiteStore) ClaimIssue(ctx context.Context, executionID, issueID, workerRef string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: claim issue %s/%s: %w", executionID, issueID, err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workers (execution_id, issue_id, worker_ref, claimed_at, last_heartbeat)
		VALUES (?, ?, ?, ?, ?)`,
		executionID, issueID, workerRef, now, now,
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

// UpdateWorkerOwner records the owning process ID and process identity
// token for an active Worker claim. It writes both together, including an
// empty ownerToken. The pid and the token must describe one process: a new
// pid beside the previous owner's token fails the identity test and makes
// the new, live owner look dead. An empty token means "identity unknown",
// which Engine.claimOwnerIsLive answers with the pid test alone.
func (s *SQLiteStore) UpdateWorkerOwner(ctx context.Context, executionID, issueID string, ownerPID int, ownerToken string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE workers
		SET owner_pid = ?, owner_token = ?
		WHERE execution_id = ? AND issue_id = ?`,
		ownerPID, ownerToken, executionID, issueID,
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

// ClearWorkerOwner zeroes owner_pid and owner_token for every active Worker
// claim owned by pid, across every Execution. A process that exits cleanly
// calls this before it exits, so a stale pid does not sit in the owner
// columns waiting for a later process to prove it is gone through the
// process-identity test (issue 457). A no-op pid (<= 0) does nothing.
func (s *SQLiteStore) ClearWorkerOwner(ctx context.Context, pid int) error {
	if pid <= 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE workers
		SET owner_pid = 0, owner_token = ''
		WHERE owner_pid = ?`,
		pid,
	); err != nil {
		return fmt.Errorf("storage: clear worker owner for pid %d: %w", pid, err)
	}
	return nil
}

// WorkerClaim reloads the active Worker claim for one Issue.
func (s *SQLiteStore) WorkerClaim(ctx context.Context, executionID, issueID string) (WorkerClaim, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT execution_id, issue_id, worker_ref, owner_pid, owner_token, claimed_at, last_heartbeat
		FROM workers
		WHERE execution_id = ? AND issue_id = ?`,
		executionID, issueID,
	)
	var claim WorkerClaim
	if err := row.Scan(&claim.ExecutionID, &claim.IssueID, &claim.WorkerRef, &claim.OwnerPID, &claim.OwnerToken, &claim.ClaimedAt, &claim.LastHeartbeat); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkerClaim{}, fmt.Errorf("storage: worker claim %s/%s: %w", executionID, issueID, ErrNotFound)
		}
		return WorkerClaim{}, fmt.Errorf("storage: load worker claim %s/%s: %w", executionID, issueID, err)
	}
	claim.ClaimedAt = claim.ClaimedAt.UTC()
	claim.LastHeartbeat = claim.LastHeartbeat.UTC()
	return claim, nil
}

func activeClaimByIssue(ctx context.Context, q querier, issueID string) (WorkerClaim, error) {
	row := q.QueryRowContext(ctx, `
		SELECT execution_id, issue_id, worker_ref, owner_pid, owner_token, claimed_at, last_heartbeat
		FROM workers
		WHERE issue_id = ?`,
		issueID,
	)
	var claim WorkerClaim
	if err := row.Scan(&claim.ExecutionID, &claim.IssueID, &claim.WorkerRef, &claim.OwnerPID, &claim.OwnerToken, &claim.ClaimedAt, &claim.LastHeartbeat); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkerClaim{}, fmt.Errorf("storage: worker claim for issue %s: %w", issueID, ErrNotFound)
		}
		return WorkerClaim{}, fmt.Errorf("storage: load worker claim for issue %s: %w", issueID, err)
	}
	claim.ClaimedAt = claim.ClaimedAt.UTC()
	claim.LastHeartbeat = claim.LastHeartbeat.UTC()
	return claim, nil
}

// ReleaseWorkerClaim removes the active Worker claim for one Issue.
func (s *SQLiteStore) ReleaseWorkerClaim(ctx context.Context, executionID, issueID string) error {
	return releaseWorkerClaim(ctx, s.db, executionID, issueID)
}

func releaseWorkerClaim(ctx context.Context, q querier, executionID, issueID string) error {
	if _, err := q.ExecContext(ctx, `
		DELETE FROM workers
		WHERE execution_id = ? AND issue_id = ?`,
		executionID, issueID,
	); err != nil {
		return fmt.Errorf("storage: release worker claim %s/%s: %w", executionID, issueID, err)
	}
	return nil
}

// HeartbeatWorker stamps the active Worker claim's last_heartbeat with at.
// Returns ErrNotFound if no active claim exists.
func (s *SQLiteStore) HeartbeatWorker(ctx context.Context, executionID, issueID string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE workers
		SET last_heartbeat = ?
		WHERE execution_id = ? AND issue_id = ?`,
		at.UTC(), executionID, issueID,
	)
	if err != nil {
		return fmt.Errorf("storage: heartbeat worker %s/%s: %w", executionID, issueID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: heartbeat worker %s/%s: %w", executionID, issueID, err)
	}
	if affected == 0 {
		return fmt.Errorf("storage: heartbeat worker %s/%s: %w", executionID, issueID, ErrNotFound)
	}
	return nil
}

func isForeignKeyConstraintErr(err error) bool {
	return strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}

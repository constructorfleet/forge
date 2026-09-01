package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Teagan42/forge/internal/domain"
)

// ErrExecutionLeaseHeld is returned by ClaimExecutionLease when the Issue
// execution already has an active lease. Enforced at the database level via
// execution_leases' primary key on (execution_id, issue_id), not by a
// read-then-write check that would race — the same pattern
// ErrPlanningLeaseHeld documents for a Feature's planning lease.
var ErrExecutionLeaseHeld = errors.New("storage: issue execution already has an active lease")

// ExecutionLeaseConflictError reports that an Issue execution's lease is
// already held, the execution-lease analogue of PlanningLeaseConflictError.
type ExecutionLeaseConflictError struct {
	ExecutionID string
	IssueID     string
}

func (e *ExecutionLeaseConflictError) Error() string {
	if e == nil {
		return ErrExecutionLeaseHeld.Error()
	}
	return "storage: execution " + e.ExecutionID + "/" + e.IssueID + " already has an active lease"
}

func (e *ExecutionLeaseConflictError) Unwrap() error { return ErrExecutionLeaseHeld }

// ExecutionLease is the active claim a remote execution holds on one Issue,
// following PlanningLease's shape plus a heartbeat and an expiry: the
// worker heartbeats through WorkerClient.Heartbeat while it works, and a
// heartbeat that lapses past ExpiresAt is how a later ticket detects a
// lost worker.
type ExecutionLease struct {
	ExecutionID string
	IssueID     string
	HeartbeatAt time.Time
	ExpiresAt   time.Time
	ClaimedAt   time.Time
}

// Lapsed reports whether the worker's heartbeat has lapsed past this
// lease's expiry as of now — the controller-side check that detects a lost
// worker (ADR 0020). now equal to ExpiresAt counts as lapsed: the lease is
// only valid strictly before its expiry.
func (l ExecutionLease) Lapsed(now time.Time) bool {
	return !now.Before(l.ExpiresAt)
}

// ClaimExecutionLease records a remote execution's lease on issueID within
// executionID, with an initial heartbeat and expiresAt. Returns
// ErrExecutionLeaseHeld (unwrappable to *ExecutionLeaseConflictError) if a
// lease is already held, or ErrNotFound if the Issue execution doesn't
// exist (execution_leases.FOREIGN KEY -> execution_issues), in both cases
// translating a database constraint violation rather than doing a
// read-then-write check that would race.
func (s *SQLiteStore) ClaimExecutionLease(ctx context.Context, executionID, issueID string, expiresAt time.Time) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO execution_leases (execution_id, issue_id, heartbeat_at, expires_at, claimed_at)
		VALUES (?, ?, ?, ?, ?)`,
		executionID, issueID, now, expiresAt.UTC(), now,
	)
	if err != nil {
		switch {
		case isUniqueConstraintErr(err):
			return fmt.Errorf("storage: claim execution lease %s/%s: %w", executionID, issueID, &ExecutionLeaseConflictError{
				ExecutionID: executionID,
				IssueID:     issueID,
			})
		case isForeignKeyConstraintErr(err):
			return fmt.Errorf("storage: claim execution lease %s/%s: %w", executionID, issueID, ErrNotFound)
		default:
			return fmt.Errorf("storage: claim execution lease %s/%s: %w", executionID, issueID, err)
		}
	}
	return nil
}

// ExecutionLease reloads the active execution lease for issueID within
// executionID. Returns ErrNotFound if no active lease exists.
func (s *SQLiteStore) ExecutionLease(ctx context.Context, executionID, issueID string) (ExecutionLease, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT execution_id, issue_id, heartbeat_at, expires_at, claimed_at
		FROM execution_leases
		WHERE execution_id = ? AND issue_id = ?`,
		executionID, issueID,
	)
	var lease ExecutionLease
	if err := row.Scan(&lease.ExecutionID, &lease.IssueID, &lease.HeartbeatAt, &lease.ExpiresAt, &lease.ClaimedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ExecutionLease{}, fmt.Errorf("storage: execution lease %s/%s: %w", executionID, issueID, ErrNotFound)
		}
		return ExecutionLease{}, fmt.Errorf("storage: load execution lease %s/%s: %w", executionID, issueID, err)
	}
	lease.HeartbeatAt = lease.HeartbeatAt.UTC()
	lease.ExpiresAt = lease.ExpiresAt.UTC()
	lease.ClaimedAt = lease.ClaimedAt.UTC()
	return lease, nil
}

// HeartbeatExecutionLease records that the worker is still alive, advancing
// the lease's heartbeat to now and its expiry to expiresAt. Returns
// ErrNotFound if no active lease exists for issueID within executionID.
func (s *SQLiteStore) HeartbeatExecutionLease(ctx context.Context, executionID, issueID string, expiresAt time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE execution_leases
		SET heartbeat_at = ?, expires_at = ?
		WHERE execution_id = ? AND issue_id = ?`,
		time.Now().UTC(), expiresAt.UTC(), executionID, issueID,
	)
	if err != nil {
		return fmt.Errorf("storage: heartbeat execution lease %s/%s: %w", executionID, issueID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: heartbeat execution lease %s/%s: %w", executionID, issueID, err)
	}
	if affected == 0 {
		return fmt.Errorf("storage: heartbeat execution lease %s/%s: %w", executionID, issueID, ErrNotFound)
	}
	return nil
}

// ListActiveExecutionLeases reloads every currently held ExecutionLease,
// ordered by claimed_at then execution_id/issue_id for stable output — the
// reconciliation loop (a later ticket under #287) polls this list to find
// leases whose heartbeat may have lapsed.
func (s *SQLiteStore) ListActiveExecutionLeases(ctx context.Context) ([]ExecutionLease, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT execution_id, issue_id, heartbeat_at, expires_at, claimed_at
		FROM execution_leases
		ORDER BY claimed_at, execution_id, issue_id`)
	if err != nil {
		return nil, fmt.Errorf("storage: list active execution leases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var leases []ExecutionLease
	for rows.Next() {
		var lease ExecutionLease
		if err := rows.Scan(&lease.ExecutionID, &lease.IssueID, &lease.HeartbeatAt, &lease.ExpiresAt, &lease.ClaimedAt); err != nil {
			return nil, fmt.Errorf("storage: list active execution leases: %w", err)
		}
		lease.HeartbeatAt = lease.HeartbeatAt.UTC()
		lease.ExpiresAt = lease.ExpiresAt.UTC()
		lease.ClaimedAt = lease.ClaimedAt.UTC()
		leases = append(leases, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list active execution leases: %w", err)
	}
	return leases, nil
}

// ReleaseExecutionLease removes the active execution lease for issueID
// within executionID. Releasing a missing lease is a no-op.
func (s *SQLiteStore) ReleaseExecutionLease(ctx context.Context, executionID, issueID string) error {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM execution_leases WHERE execution_id = ? AND issue_id = ?`,
		executionID, issueID,
	); err != nil {
		return fmt.Errorf("storage: release execution lease %s/%s: %w", executionID, issueID, err)
	}
	return nil
}

// ExecutionPlacement records the remote-execution substrate facts for one
// Issue execution: the backend and worker that ran it, the Workspace the
// worker prepared, and the current workspace-lifecycle state. It is kept
// separate from IssueState (internal/domain/state.go): LOST is a
// workspace-lifecycle state, not an IssueState.
type ExecutionPlacement struct {
	ExecutionID string
	IssueID     string
	Backend     string
	WorkerRef   string
	Workspace   domain.Workspace
	Lifecycle   domain.WorkspaceLifecycle
}

// RecordExecutionPlacement persists placement, replacing any earlier record
// for the same Execution/Issue pair.
func (s *SQLiteStore) RecordExecutionPlacement(ctx context.Context, placement ExecutionPlacement) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO execution_placements (execution_id, issue_id, backend, worker_ref, workspace_path, workspace_branch, lifecycle)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(execution_id, issue_id) DO UPDATE SET
			backend = excluded.backend,
			worker_ref = excluded.worker_ref,
			workspace_path = excluded.workspace_path,
			workspace_branch = excluded.workspace_branch,
			lifecycle = excluded.lifecycle`,
		placement.ExecutionID, placement.IssueID, placement.Backend, placement.WorkerRef,
		placement.Workspace.Path, placement.Workspace.Branch, string(placement.Lifecycle),
	)
	if err != nil {
		return fmt.Errorf("storage: record execution placement %s/%s: %w", placement.ExecutionID, placement.IssueID, err)
	}
	return nil
}

// ExecutionPlacementByIssue reloads the persisted execution placement for
// issueID within executionID. Returns ErrNotFound if none has been
// recorded.
func (s *SQLiteStore) ExecutionPlacementByIssue(ctx context.Context, executionID, issueID string) (ExecutionPlacement, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT execution_id, issue_id, backend, worker_ref, workspace_path, workspace_branch, lifecycle
		FROM execution_placements
		WHERE execution_id = ? AND issue_id = ?`,
		executionID, issueID,
	)
	var (
		placement ExecutionPlacement
		lifecycle string
	)
	if err := row.Scan(
		&placement.ExecutionID, &placement.IssueID, &placement.Backend, &placement.WorkerRef,
		&placement.Workspace.Path, &placement.Workspace.Branch, &lifecycle,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ExecutionPlacement{}, fmt.Errorf("storage: execution placement %s/%s: %w", executionID, issueID, ErrNotFound)
		}
		return ExecutionPlacement{}, fmt.Errorf("storage: load execution placement %s/%s: %w", executionID, issueID, err)
	}
	placement.Workspace.IssueID = issueID
	placement.Lifecycle = domain.WorkspaceLifecycle(lifecycle)
	return placement, nil
}

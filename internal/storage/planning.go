package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Teagan42/forge/internal/domain"
)

// ErrPlanningLeaseHeld is returned by ClaimFeaturePlanningLease when the
// Feature already has an active planning lease. Enforced at the database
// level via feature_planning_leases' primary key on feature_id, not by a
// read-then-write check that would race — the same pattern ErrAlreadyClaimed
// documents for Worker claims.
var ErrPlanningLeaseHeld = errors.New("storage: feature already has an active planning lease")

// PlanningLeaseConflictError reports that a Feature's planning lease is
// already held by another Planning Execution, the lease analogue of
// ClaimConflictError.
type PlanningLeaseConflictError struct {
	FeatureID         string
	OwningExecutionID string
}

func (e *PlanningLeaseConflictError) Error() string {
	if e == nil || e.OwningExecutionID == "" {
		return ErrPlanningLeaseHeld.Error()
	}
	return "storage: feature " + e.FeatureID + " already has an active planning lease held by execution " + e.OwningExecutionID
}

func (e *PlanningLeaseConflictError) Unwrap() error { return ErrPlanningLeaseHeld }

// PlanningLease is the active Feature-scoped planning claim, including the
// owning process ID abandoned-lease recovery uses to distinguish a live
// `forge plan` process from an orphaned one after a crash or termination —
// the planning analogue of WorkerClaim.
//
// OwnerToken identifies the owning process itself, so a recycled pid does
// not look like the same live owner (issue 557). It is empty for leases
// claimed before migration 0032 added the column, or when the process
// identity lookup fails; callers then fall back to a pid liveness test,
// mirroring WorkerClaim.OwnerToken.
type PlanningLease struct {
	FeatureID   string
	ExecutionID string
	OwnerPID    int
	OwnerToken  string
	ClaimedAt   time.Time
}

// CreatePlanningExecution persists a new Planning Execution.
func (s *SQLiteStore) CreatePlanningExecution(ctx context.Context, exec domain.PlanningExecution) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO planning_executions (id, feature_id, base_revision, status, started_at)
		VALUES (?, ?, ?, ?, ?)`,
		exec.ID, exec.FeatureID, exec.BaseRevision, string(exec.Status), exec.StartedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("storage: create planning execution %s: %w", exec.ID, err)
	}
	return nil
}

// LoadPlanningExecution reloads a single Planning Execution by ID.
func (s *SQLiteStore) LoadPlanningExecution(ctx context.Context, executionID string) (domain.PlanningExecution, error) {
	return s.getPlanningExecution(ctx, s.db, executionID)
}

func (s *SQLiteStore) getPlanningExecution(ctx context.Context, q querier, executionID string) (domain.PlanningExecution, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, feature_id, base_revision, status, started_at
		FROM planning_executions WHERE id = ?`,
		executionID,
	)
	return scanPlanningExecution(row)
}

// ListPlanningExecutionsByFeature reloads every Planning Execution recorded
// for featureID, ordered by started_at then id for stable output.
func (s *SQLiteStore) ListPlanningExecutionsByFeature(ctx context.Context, featureID string) ([]domain.PlanningExecution, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, feature_id, base_revision, status, started_at
		FROM planning_executions
		WHERE feature_id = ?
		ORDER BY started_at, id`,
		featureID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list planning executions for feature %s: %w", featureID, err)
	}
	defer func() { _ = rows.Close() }()

	var executions []domain.PlanningExecution
	for rows.Next() {
		exec, err := scanPlanningExecution(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: list planning executions for feature %s: %w", featureID, err)
		}
		executions = append(executions, exec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list planning executions for feature %s: %w", featureID, err)
	}
	return executions, nil
}

func scanPlanningExecution(row scanner) (domain.PlanningExecution, error) {
	var (
		exec      domain.PlanningExecution
		status    string
		startedAt time.Time
	)
	if err := row.Scan(&exec.ID, &exec.FeatureID, &exec.BaseRevision, &status, &startedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.PlanningExecution{}, ErrNotFound
		}
		return domain.PlanningExecution{}, err
	}
	exec.Status = domain.PlanningStatus(status)
	exec.StartedAt = startedAt.UTC()
	return exec, nil
}

// UpdatePlanningStatus persists status as executionID's current runtime
// Status. Stage and artifact freshness are never persisted here — they are
// derived from the Feature's Planning Artifacts on disk (internal/planning)
// every time they're needed.
func (s *SQLiteStore) UpdatePlanningStatus(ctx context.Context, executionID string, status domain.PlanningStatus) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE planning_executions SET status = ? WHERE id = ?`,
		string(status), executionID,
	)
	if err != nil {
		return fmt.Errorf("storage: update planning status for execution %s: %w", executionID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: update planning status for execution %s: %w", executionID, err)
	}
	if affected == 0 {
		return fmt.Errorf("storage: update planning status for execution %s: %w", executionID, ErrNotFound)
	}
	return nil
}

// ClaimFeaturePlanningLease records a Planning Execution's lease on
// featureID. Returns ErrPlanningLeaseHeld if the Feature already has an
// active lease (feature_planning_leases.PRIMARY KEY(feature_id)) — as
// *PlanningLeaseConflictError, so callers can report the owning execution —
// or ErrNotFound if executionID doesn't exist
// (feature_planning_leases.FOREIGN KEY -> planning_executions), in both
// cases translating a database constraint violation rather than doing a
// read-then-write check that would race.
func (s *SQLiteStore) ClaimFeaturePlanningLease(ctx context.Context, featureID, executionID string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO feature_planning_leases (feature_id, execution_id, owner_pid, claimed_at)
		VALUES (?, ?, 0, ?)`,
		featureID, executionID, now,
	)
	if err != nil {
		switch {
		case isUniqueConstraintErr(err):
			lease, loadErr := s.FeaturePlanningLease(ctx, featureID)
			if loadErr != nil {
				return fmt.Errorf("storage: claim planning lease for feature %s: %w", featureID, ErrPlanningLeaseHeld)
			}
			return fmt.Errorf("storage: claim planning lease for feature %s: %w", featureID, &PlanningLeaseConflictError{
				FeatureID:         featureID,
				OwningExecutionID: lease.ExecutionID,
			})
		case isForeignKeyConstraintErr(err):
			return fmt.Errorf("storage: claim planning lease for feature %s: %w", featureID, ErrNotFound)
		default:
			return fmt.Errorf("storage: claim planning lease for feature %s: %w", featureID, err)
		}
	}
	return nil
}

// FeaturePlanningLease reloads the active planning lease for featureID.
// Returns ErrNotFound if no active lease exists.
func (s *SQLiteStore) FeaturePlanningLease(ctx context.Context, featureID string) (PlanningLease, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT feature_id, execution_id, owner_pid, owner_token, claimed_at
		FROM feature_planning_leases
		WHERE feature_id = ?`,
		featureID,
	)
	var lease PlanningLease
	if err := row.Scan(&lease.FeatureID, &lease.ExecutionID, &lease.OwnerPID, &lease.OwnerToken, &lease.ClaimedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PlanningLease{}, fmt.Errorf("storage: planning lease for feature %s: %w", featureID, ErrNotFound)
		}
		return PlanningLease{}, fmt.Errorf("storage: load planning lease for feature %s: %w", featureID, err)
	}
	lease.ClaimedAt = lease.ClaimedAt.UTC()
	return lease, nil
}

// UpdatePlanningLeaseOwner records the OS process ID and process identity
// token currently owning the active planning lease for featureID. It writes
// both together, including an empty ownerToken. The pid and the token must
// describe one process: a new pid beside the previous owner's token fails
// the identity test and makes the new, live owner look dead. An empty token
// means "identity unknown", which planengine's liveness check then answers
// with the pid test alone — the planning analogue of UpdateWorkerOwner.
func (s *SQLiteStore) UpdatePlanningLeaseOwner(ctx context.Context, featureID string, ownerPID int, ownerToken string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE feature_planning_leases SET owner_pid = ?, owner_token = ? WHERE feature_id = ?`,
		ownerPID, ownerToken, featureID,
	)
	if err != nil {
		return fmt.Errorf("storage: update planning lease owner for feature %s: %w", featureID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: update planning lease owner for feature %s: %w", featureID, err)
	}
	if affected == 0 {
		return fmt.Errorf("storage: update planning lease owner for feature %s: %w", featureID, ErrNotFound)
	}
	return nil
}

// ReleaseFeaturePlanningLease removes the active planning lease for
// featureID. Releasing a missing lease is a no-op.
func (s *SQLiteStore) ReleaseFeaturePlanningLease(ctx context.Context, featureID string) error {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM feature_planning_leases WHERE feature_id = ?`,
		featureID,
	); err != nil {
		return fmt.Errorf("storage: release planning lease for feature %s: %w", featureID, err)
	}
	return nil
}

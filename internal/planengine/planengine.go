// Package planengine implements Planning Execution's runtime container
// (ticket 11): starting, resuming, and finishing a `forge plan` run for a
// Feature. It reuses the same claim-via-unique-constraint and PID-liveness
// patterns internal/engine uses for coding Executions and Worker claims,
// but scoped to Features and feature_planning_leases rather than Issues and
// workers — Implementation Issue claims are a separate table and are never
// touched here.
package planengine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/wayfinding"
)

// Runtime starts, resumes, and finishes Planning Executions, serializing
// them per Feature via feature_planning_leases.
type Runtime struct {
	Store storage.Store

	// Now, NewExecutionID, OwnerPID, and ProcessRunning are seams for
	// deterministic tests; New sets all four to real implementations,
	// mirroring internal/engine.Engine's identical seams.
	Now            func() time.Time
	NewExecutionID func() string
	OwnerPID       func() int
	ProcessRunning func(pid int) (bool, error)
}

// New builds a Runtime from its injected Store.
func New(store storage.Store) *Runtime {
	return &Runtime{
		Store:          store,
		Now:            func() time.Time { return time.Now().UTC() },
		NewExecutionID: func() string { return uuid.NewString() },
		OwnerPID:       os.Getpid,
		ProcessRunning: processRunning,
	}
}

func processRunning(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	case errors.Is(err, syscall.EPERM):
		return true, nil
	default:
		return false, err
	}
}

// Start begins a Planning Execution for featureID rooted at baseRevision,
// enforcing at most one active Planning Execution per Feature.
//
//   - If featureID has no lease, a fresh Planning Execution is created and
//     claimed.
//   - If featureID's lease is held by a live process other than this one,
//     Start returns an error wrapping *storage.PlanningLeaseConflictError —
//     "a user running forge plan on a goal-only Feature" a second time
//     while a first run is still active is rejected rather than racing it.
//   - If featureID's lease belongs to a dead process (an abandoned lease
//     from a crash or termination) or to this same process (a restart),
//     the existing non-terminal Planning Execution is reclaimed and
//     returned unchanged — this is how a Planning Execution "survives a
//     restart".
//   - If the existing Planning Execution has already reached a terminal
//     Status (COMPLETE/FAILED) despite still holding a lease, the lease is
//     released and a fresh Planning Execution is started.
func (r *Runtime) Start(ctx context.Context, featureID, baseRevision string) (domain.PlanningExecution, error) {
	lease, err := r.Store.FeaturePlanningLease(ctx, featureID)
	if errors.Is(err, storage.ErrNotFound) {
		return r.startNew(ctx, featureID, baseRevision)
	}
	if err != nil {
		return domain.PlanningExecution{}, fmt.Errorf("planengine: load planning lease for feature %s: %w", featureID, err)
	}

	running, err := r.ProcessRunning(lease.OwnerPID)
	if err != nil {
		return domain.PlanningExecution{}, fmt.Errorf("planengine: inspect planning lease owner for feature %s: %w", featureID, err)
	}
	if running && lease.OwnerPID != r.OwnerPID() {
		return domain.PlanningExecution{}, fmt.Errorf("planengine: feature %s planning is still owned by live process %d: %w",
			featureID, lease.OwnerPID, &storage.PlanningLeaseConflictError{FeatureID: featureID, OwningExecutionID: lease.ExecutionID})
	}

	exec, err := r.Store.LoadPlanningExecution(ctx, lease.ExecutionID)
	if err != nil {
		return domain.PlanningExecution{}, fmt.Errorf("planengine: load planning execution %s: %w", lease.ExecutionID, err)
	}
	if exec.Status.IsTerminal() {
		if err := r.Store.ReleaseFeaturePlanningLease(ctx, featureID); err != nil {
			return domain.PlanningExecution{}, fmt.Errorf("planengine: release finished planning lease for feature %s: %w", featureID, err)
		}
		return r.startNew(ctx, featureID, baseRevision)
	}

	if err := r.Store.UpdatePlanningLeaseOwner(ctx, featureID, r.OwnerPID()); err != nil {
		return domain.PlanningExecution{}, fmt.Errorf("planengine: reclaim planning lease for feature %s: %w", featureID, err)
	}
	return exec, nil
}

func (r *Runtime) startNew(ctx context.Context, featureID, baseRevision string) (domain.PlanningExecution, error) {
	exec := domain.PlanningExecution{
		ID:           r.NewExecutionID(),
		FeatureID:    featureID,
		BaseRevision: baseRevision,
		Status:       domain.PlanningStatusActive,
		StartedAt:    r.Now(),
	}
	if err := r.Store.CreatePlanningExecution(ctx, exec); err != nil {
		return domain.PlanningExecution{}, fmt.Errorf("planengine: create planning execution for feature %s: %w", featureID, err)
	}
	if err := r.appendEvent(ctx, exec.ID, "planning.started", struct {
		FeatureID    string `json:"feature_id"`
		BaseRevision string `json:"base_revision"`
	}{FeatureID: featureID, BaseRevision: baseRevision}); err != nil {
		return domain.PlanningExecution{}, fmt.Errorf("planengine: record planning started event for feature %s: %w", featureID, err)
	}
	if err := r.Store.ClaimFeaturePlanningLease(ctx, featureID, exec.ID); err != nil {
		return domain.PlanningExecution{}, fmt.Errorf("planengine: claim planning lease for feature %s: %w", featureID, err)
	}
	if err := r.Store.UpdatePlanningLeaseOwner(ctx, featureID, r.OwnerPID()); err != nil {
		return domain.PlanningExecution{}, fmt.Errorf("planengine: record planning lease owner for feature %s: %w", featureID, err)
	}
	return exec, nil
}

// Finish records executionID's terminal Status and releases featureID's
// planning lease, letting a subsequent Start begin a fresh Planning
// Execution for the Feature.
func (r *Runtime) Finish(ctx context.Context, featureID, executionID string, status domain.PlanningStatus) error {
	if err := r.Store.UpdatePlanningStatus(ctx, executionID, status); err != nil {
		return fmt.Errorf("planengine: finish planning execution %s: %w", executionID, err)
	}
	if err := r.appendEvent(ctx, executionID, "planning.finished", struct {
		Status string `json:"status"`
	}{Status: string(status)}); err != nil {
		return fmt.Errorf("planengine: record planning finished event for execution %s: %w", executionID, err)
	}
	if err := r.Store.ReleaseFeaturePlanningLease(ctx, featureID); err != nil {
		return fmt.Errorf("planengine: release planning lease for feature %s: %w", featureID, err)
	}
	return nil
}

// appendEvent records a planning-scoped Event with a JSON-encoded payload,
// timestamped by r.Now, via storage.MarshalEvent so this shares its
// marshal-then-construct step with internal/wayfinding's checkpoint-scoped
// Event appends rather than duplicating it. Planning Events carry no
// IssueID: a Feature being planned has no execution_issues row for them to
// reference.
func (r *Runtime) appendEvent(ctx context.Context, executionID, eventType string, payload any) error {
	event, err := storage.MarshalEvent(executionID, eventType, r.Now(), payload)
	if err != nil {
		return err
	}
	return r.Store.AppendEvent(ctx, event)
}

// ResumePlanningExecution resumes a paused Planning Execution by checking
// for new human input on any NEEDS_HUMAN decisions and transitioning the
// execution back to ACTIVE if any decision has new human comments.
//
// The tracker is used to fetch comments on the Feature's tracker issue.
// Returns the updated PlanningExecution and a boolean indicating whether
// any decision was resumed (i.e. new human input was found).
func (r *Runtime) ResumePlanningExecution(ctx context.Context, executionID string, trk tracker.Tracker) (domain.PlanningExecution, bool, error) {
	exec, err := r.Store.LoadPlanningExecution(ctx, executionID)
	if err != nil {
		return domain.PlanningExecution{}, false, fmt.Errorf("planengine: resume planning execution %s: %w", executionID, err)
	}

	if exec.Status != domain.PlanningStatusNeedsHuman {
		// Not paused, nothing to resume
		return exec, false, nil
	}

	// Get all decision checkpoints for this execution that are in NEEDS_HUMAN
	checkpoints, err := r.Store.GetDecisionCheckpointsByExecution(ctx, executionID)
	if err != nil {
		return domain.PlanningExecution{}, false, fmt.Errorf("planengine: resume planning execution %s: load checkpoints: %w", executionID, err)
	}

	anyResumed := false
	for _, checkpoint := range checkpoints {
		// Only process checkpoints that haven't been resumed yet
		if checkpoint.ResumedAt != nil {
			continue
		}

		// Use a fake tracker that wraps the real tracker to match the ResumeDecisionTracker interface
		resumeTracker := &planningResumeTracker{Tracker: trk}
		result, err := wayfinding.ResumeDecision(ctx, r.Store, resumeTracker, executionID, checkpoint.DecisionID, r.Now)
		if err != nil {
			return domain.PlanningExecution{}, false, fmt.Errorf("planengine: resume decision %s: %w", checkpoint.DecisionID, err)
		}
		if result.Resumed {
			anyResumed = true
		}
	}

	// Reload the execution to get the updated status
	exec, err = r.Store.LoadPlanningExecution(ctx, executionID)
	if err != nil {
		return domain.PlanningExecution{}, false, fmt.Errorf("planengine: resume planning execution %s: reload: %w", executionID, err)
	}

	return exec, anyResumed, nil
}

// planningResumeTracker adapts tracker.Tracker to wayfinding.ResumeDecisionTracker.
type planningResumeTracker struct {
	tracker.Tracker
}

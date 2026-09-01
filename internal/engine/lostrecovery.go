package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

// LostRecoveryStore is the subset of storage.Store RecoverLostExecution
// needs — a narrower interface than storage.Store, for the same reason
// ResumeStore is narrower (see resume.go).
type LostRecoveryStore interface {
	ExecutionLease(ctx context.Context, executionID, issueID string) (storage.ExecutionLease, error)
	ReleaseExecutionLease(ctx context.Context, executionID, issueID string) error
	ExecutionPlacementByIssue(ctx context.Context, executionID, issueID string) (storage.ExecutionPlacement, error)
	RecordExecutionPlacement(ctx context.Context, placement storage.ExecutionPlacement) error
	GetIssue(ctx context.Context, executionID, issueID string) (domain.Issue, error)
	UpdateRetryBudget(ctx context.Context, executionID, issueID string, budget domain.RetryBudget) error
}

// LostRecoveryResult is the outcome of RecoverLostExecution.
type LostRecoveryResult struct {
	// Lost is true when the worker's heartbeat had lapsed past its lease's
	// expiry — i.e. an active lease was found and it was expired.
	Lost bool

	// Retried is true only when Lost is true and the Issue's retry budget
	// still had room, so the Issue is expected to run again. False both
	// when there was nothing to recover (Lost is also false) and when the
	// retry budget was exhausted (Lost is true, the error is non-nil).
	Retried bool

	// Issue is the Issue's state after RecoverLostExecution returns. Its
	// IssueState is always left unchanged (LOST is an execution-substrate
	// concept, not an IssueState — ADR 0020); only its RetryBudget may
	// advance.
	Issue domain.Issue
}

// RecoverLostExecution checks whether the ExecutionLease held by
// executionID/issueID has lapsed past its expiry
// (storage.ExecutionLease.Lapsed), as of now(). If no lease is held, or the
// heartbeat has not lapsed, it does nothing and returns a zero-value
// LostRecoveryResult.
//
// When the heartbeat has lapsed, the controller treats the worker as lost.
// It expires the lease (ReleaseExecutionLease) and marks the execution's
// ExecutionPlacement non-authoritative (Lifecycle set to
// domain.WorkspaceLifecycleLost). It then retries the Issue against its
// existing retry budget, not a new one (ADR 0020, ADR 0023, CONTEXT.md
// "Retry Budget"): it reuses the gate-failure counter, since a lost worker
// is an incomplete attempt that must redo the same work, like a gate
// failure. If the gate counter is already exhausted, RecoverLostExecution
// returns a *domain.RetryExhaustedError and Retried is false. The caller
// must not retry again. The Issue's IssueState stays exactly as it was: the
// Issue stays in its existing states across the LOST and retry path.
func RecoverLostExecution(ctx context.Context, store LostRecoveryStore, executionID, issueID string, now func() time.Time) (LostRecoveryResult, error) {
	lease, err := store.ExecutionLease(ctx, executionID, issueID)
	if errors.Is(err, storage.ErrNotFound) {
		return LostRecoveryResult{}, nil
	}
	if err != nil {
		return LostRecoveryResult{}, fmt.Errorf("engine: load execution lease %s/%s: %w", executionID, issueID, err)
	}
	if !lease.Lapsed(now()) {
		return LostRecoveryResult{}, nil
	}

	if err := store.ReleaseExecutionLease(ctx, executionID, issueID); err != nil {
		return LostRecoveryResult{}, fmt.Errorf("engine: expire lease for lost execution %s/%s: %w", executionID, issueID, err)
	}

	if err := markPlacementLost(ctx, store, executionID, issueID); err != nil {
		return LostRecoveryResult{}, err
	}

	issue, err := store.GetIssue(ctx, executionID, issueID)
	if err != nil {
		return LostRecoveryResult{}, fmt.Errorf("engine: load issue %s for lost-execution retry: %w", issueID, err)
	}

	if err := issue.RecordGateFailure(); err != nil {
		return LostRecoveryResult{Lost: true, Issue: issue}, err
	}
	if err := store.UpdateRetryBudget(ctx, executionID, issueID, issue.RetryBudget); err != nil {
		return LostRecoveryResult{}, fmt.Errorf("engine: persist retry budget for lost execution %s/%s: %w", executionID, issueID, err)
	}

	return LostRecoveryResult{Lost: true, Retried: true, Issue: issue}, nil
}

// markPlacementLost sets the persisted ExecutionPlacement's Lifecycle to
// LOST, leaving every other field unchanged. A missing placement is a
// no-op: some remote executions may not have recorded one yet, and a
// vanished worker is still LOST regardless.
func markPlacementLost(ctx context.Context, store LostRecoveryStore, executionID, issueID string) error {
	placement, err := store.ExecutionPlacementByIssue(ctx, executionID, issueID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("engine: load execution placement for lost execution %s/%s: %w", executionID, issueID, err)
	}
	placement.Lifecycle = domain.WorkspaceLifecycleLost
	if err := store.RecordExecutionPlacement(ctx, placement); err != nil {
		return fmt.Errorf("engine: mark execution placement lost for %s/%s: %w", executionID, issueID, err)
	}
	return nil
}

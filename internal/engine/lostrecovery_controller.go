package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

// LeaseLister is the subset of storage.Store LostExecutionController needs
// to enumerate every active ExecutionLease to check for loss — a narrower
// interface than storage.Store, the same pattern as LostRecoveryStore.
type LeaseLister interface {
	ListActiveExecutionLeases(ctx context.Context) ([]storage.ExecutionLease, error)
}

// ExecutionResumer is the subset of *Engine LostExecutionController needs
// to re-enter the Prepare/Execute path for a retried Issue against a fresh
// Workspace, exactly as `forge resume` does after an orchestrator restart.
type ExecutionResumer interface {
	ResumeExecution(ctx context.Context, executionID string) (storage.ExecutionState, error)
}

// LostExecutionController is the controller reconciliation loop that
// drives RecoverLostExecution: it periodically polls every active
// ExecutionLease, retries a lapsed one under its existing retry budget,
// and — for every Issue a retry succeeded for — redispatches its
// Execution through Resumer so the retried Issue re-enters Prepare/Execute
// against a fresh Workspace (ADR 0020, ADR 0023).
type LostExecutionController struct {
	Store   LostRecoveryStore
	Leases  LeaseLister
	Resumer ExecutionResumer
	Now     func() time.Time

	// Sleep waits between reconciliation passes in Run. Defaults to a
	// real timer (set by NewLostExecutionController); overridable for
	// deterministic tests, the same pattern as ci.Supervisor.Sleep.
	Sleep func(ctx context.Context, d time.Duration) error
}

// NewLostExecutionController returns a LostExecutionController wired to
// store, backed by a real timer for Run's between-pass wait.
func NewLostExecutionController(store LostRecoveryStore, leases LeaseLister, resumer ExecutionResumer, now func() time.Time) *LostExecutionController {
	return &LostExecutionController{
		Store:   store,
		Leases:  leases,
		Resumer: resumer,
		Now:     now,
		Sleep: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

// ReconcileOnce runs a single reconciliation pass over every active
// ExecutionLease: it calls RecoverLostExecution for each, then — once per
// distinct Execution with at least one successfully retried Issue — calls
// Resumer.ResumeExecution to redispatch it.
//
// A lease whose retry budget is exhausted (*domain.RetryExhaustedError) is
// an expected, terminal outcome and does not abort the pass. Any other
// RecoverLostExecution or Resumer failure is collected and returned once
// the pass completes (via errors.Join), so one bad lease or Execution
// never stops the rest of the pass from running.
func (c *LostExecutionController) ReconcileOnce(ctx context.Context) ([]LostRecoveryResult, error) {
	leases, err := c.Leases.ListActiveExecutionLeases(ctx)
	if err != nil {
		return nil, fmt.Errorf("engine: list active execution leases: %w", err)
	}

	var (
		results  []LostRecoveryResult
		errs     error
		toResume []string
		seen     = map[string]bool{}
	)

	for _, lease := range leases {
		result, err := RecoverLostExecution(ctx, c.Store, lease.ExecutionID, lease.IssueID, c.Now)
		var exhausted *domain.RetryExhaustedError
		if err != nil && !errors.As(err, &exhausted) {
			errs = errors.Join(errs, fmt.Errorf("engine: recover lost execution %s/%s: %w", lease.ExecutionID, lease.IssueID, err))
			continue
		}
		results = append(results, result)
		if result.Retried && !seen[lease.ExecutionID] {
			seen[lease.ExecutionID] = true
			toResume = append(toResume, lease.ExecutionID)
		}
	}

	for _, executionID := range toResume {
		if _, err := c.Resumer.ResumeExecution(ctx, executionID); err != nil {
			errs = errors.Join(errs, fmt.Errorf("engine: resume retried execution %s: %w", executionID, err))
		}
	}

	return results, errs
}

// Run drives ReconcileOnce repeatedly, waiting interval between passes via
// Sleep, until ctx is cancelled — Sleep then returns ctx.Err() and Run
// returns it. onErr, when non-nil, receives every ReconcileOnce error;
// Run keeps looping after a failed pass rather than aborting, so one bad
// pass never stops later passes from running.
func (c *LostExecutionController) Run(ctx context.Context, interval time.Duration, onErr func(error)) error {
	for {
		if _, err := c.ReconcileOnce(ctx); err != nil && onErr != nil {
			onErr(err)
		}
		if err := c.Sleep(ctx, interval); err != nil {
			return err
		}
	}
}

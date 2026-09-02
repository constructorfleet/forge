package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Teagan42/forge/internal/domain"
)

// ProviderLimitRecoveryStore is the subset of storage.Store
// ProviderLimitController needs — a narrower interface than storage.Store,
// the same pattern LostRecoveryStore and LeaseLister follow.
type ProviderLimitRecoveryStore interface {
	ListDueProviderLimitIssues(ctx context.Context, now time.Time) ([]domain.Issue, error)
	TransitionIssue(ctx context.Context, executionID, issueID string, to domain.IssueState) (domain.Issue, error)
	ScheduleProviderLimitRetry(ctx context.Context, executionID, issueID string, retryAt *time.Time) error
}

// ProviderLimitRecoveryResult is the outcome of one due Issue in a
// ProviderLimitController pass.
type ProviderLimitRecoveryResult struct {
	// Retried is true when the Issue returned to READY, so it is expected to
	// run again.
	Retried bool

	// Exhausted is true when the provider-limit retry budget had no room
	// left, so the Issue moved to FAILED instead. Retried is then false.
	Exhausted bool

	// Issue is the Issue's state after the pass handled it.
	Issue domain.Issue
}

// ProviderLimitController is the controller reconciliation loop for Issues
// parked in PROVIDER_LIMIT (issue 423). It periodically lists every Issue
// whose backoff deadline has passed, returns each to READY under its existing
// provider-limit retry budget, and redispatches each affected Execution
// through Resumer, so the retried Issue re-enters Prepare/Execute.
//
// It has the same shape as LostExecutionController on purpose: both recover
// work that stopped for a reason outside the Agent's control, and both
// redispatch once per distinct Execution.
type ProviderLimitController struct {
	Store   ProviderLimitRecoveryStore
	Resumer ExecutionResumer
	Now     func() time.Time

	// Sleep waits between reconciliation passes in Run. Defaults to a real
	// timer (set by NewProviderLimitController); overridable for
	// deterministic tests, the same pattern LostExecutionController uses.
	Sleep func(ctx context.Context, d time.Duration) error
}

// NewProviderLimitController returns a ProviderLimitController wired to
// store, backed by a real timer for Run's between-pass wait.
func NewProviderLimitController(store ProviderLimitRecoveryStore, resumer ExecutionResumer, now func() time.Time) *ProviderLimitController {
	return &ProviderLimitController{
		Store:   store,
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

// ReconcileOnce runs a single reconciliation pass over every due Issue: it
// retries each, then — once per distinct Execution with at least one retried
// Issue — calls Resumer.ResumeExecution to redispatch it.
//
// An Issue whose provider-limit retry budget is exhausted moves to FAILED and
// is reported with Exhausted set. That is an expected, terminal outcome and
// does not abort the pass. Any other failure is collected and returned once
// the pass completes (via errors.Join), so one bad Issue or Execution never
// stops the rest of the pass from running.
func (c *ProviderLimitController) ReconcileOnce(ctx context.Context) ([]ProviderLimitRecoveryResult, error) {
	due, err := c.Store.ListDueProviderLimitIssues(ctx, c.Now())
	if err != nil {
		return nil, fmt.Errorf("engine: list due provider-limit issues: %w", err)
	}

	var (
		results  []ProviderLimitRecoveryResult
		errs     error
		toResume []string
		seen     = map[string]bool{}
	)

	for _, issue := range due {
		result, err := c.recover(ctx, issue)
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		results = append(results, result)
		if result.Retried && !seen[issue.ExecutionID] {
			seen[issue.ExecutionID] = true
			toResume = append(toResume, issue.ExecutionID)
		}
	}

	for _, executionID := range toResume {
		if _, err := c.Resumer.ResumeExecution(ctx, executionID); err != nil {
			errs = errors.Join(errs, fmt.Errorf("engine: resume retried execution %s: %w", executionID, err))
		}
	}

	return results, errs
}

// recover handles one due Issue. The backoff deadline is always cleared: a
// retried Issue no longer waits, and a failed Issue never waits again.
func (c *ProviderLimitController) recover(ctx context.Context, issue domain.Issue) (ProviderLimitRecoveryResult, error) {
	to := domain.StateReady
	exhausted := issue.RetryBudget.ProviderLimitExhausted()
	if exhausted {
		to = domain.StateFailed
	}

	updated, err := c.Store.TransitionIssue(ctx, issue.ExecutionID, issue.ID, to)
	if err != nil {
		return ProviderLimitRecoveryResult{}, fmt.Errorf(
			"engine: retry provider-limit issue %s/%s: %w", issue.ExecutionID, issue.ID, err)
	}
	if err := c.Store.ScheduleProviderLimitRetry(ctx, issue.ExecutionID, issue.ID, nil); err != nil {
		return ProviderLimitRecoveryResult{}, fmt.Errorf(
			"engine: clear provider-limit deadline for issue %s/%s: %w", issue.ExecutionID, issue.ID, err)
	}
	updated.ProviderLimitRetryAt = nil

	return ProviderLimitRecoveryResult{
		Retried:   !exhausted,
		Exhausted: exhausted,
		Issue:     updated,
	}, nil
}

// Run drives ReconcileOnce repeatedly, waiting interval between passes via
// Sleep, until ctx is cancelled — Sleep then returns ctx.Err() and Run
// returns it. onErr, when non-nil, receives every ReconcileOnce error; Run
// keeps looping after a failed pass rather than aborting, so one bad pass
// never stops later passes from running.
func (c *ProviderLimitController) Run(ctx context.Context, interval time.Duration, onErr func(error)) error {
	for {
		if _, err := c.ReconcileOnce(ctx); err != nil && onErr != nil {
			onErr(err)
		}
		if err := c.Sleep(ctx, interval); err != nil {
			return err
		}
	}
}

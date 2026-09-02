package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/storage"
)

// seedParkedProviderLimitIssue creates an Execution (once per ID) with one
// Issue parked in PROVIDER_LIMIT and a persisted backoff deadline.
func seedParkedProviderLimitIssue(t *testing.T, store *storage.SQLiteStore, executionID, issueID string, limit, used int, retryAt *time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.LoadExecution(ctx, executionID); err != nil {
		if err := store.CreateExecution(ctx, domain.Execution{ID: executionID, BaseRevision: "base", StartedAt: time.Now().UTC()}); err != nil {
			t.Fatalf("CreateExecution: %v", err)
		}
	}
	issue := domain.Issue{
		ID: issueID, ExecutionID: executionID, State: domain.StateReady, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudgetFrom(
			domain.RetryLimits{Gate: 3, Review: 3, CI: 3, ProviderLimit: limit},
			0, 0, 0, used,
		),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	// READY -> CLAIMED -> ... is not needed: the controller only cares that
	// the row is in PROVIDER_LIMIT, so the state is set directly through the
	// legal IMPLEMENTING path.
	for _, to := range []domain.IssueState{
		domain.StateClaimed, domain.StatePreparing, domain.StateImplementing, domain.StateProviderLimit,
	} {
		if _, err := store.TransitionIssue(ctx, executionID, issueID, to); err != nil {
			t.Fatalf("TransitionIssue(%s): %v", to, err)
		}
	}
	if err := store.ScheduleProviderLimitRetry(ctx, executionID, issueID, retryAt); err != nil {
		t.Fatalf("ScheduleProviderLimitRetry: %v", err)
	}
}

// TestProviderLimitControllerRetriesDueIssue is the headline test: a parked
// Issue whose backoff time has passed returns to READY, loses its deadline,
// and its Execution is redispatched exactly once.
func TestProviderLimitControllerRetriesDueIssue(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	seedParkedProviderLimitIssue(t, store, "exec-1", "issue-1", 3, 1, &past)
	seedParkedProviderLimitIssue(t, store, "exec-1", "issue-2", 3, 1, &past)

	resumer := &fakeExecutionResumer{}
	controller := engine.NewProviderLimitController(store, resumer, func() time.Time { return now })

	results, err := controller.ReconcileOnce(ctx)
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %+v", results)
	}
	for _, res := range results {
		if !res.Retried {
			t.Fatalf("expected every due issue to be retried, got %+v", res)
		}
		if res.Issue.State != domain.StateReady {
			t.Fatalf("retried issue state = %s, want READY", res.Issue.State)
		}
	}
	for _, issueID := range []string{"issue-1", "issue-2"} {
		reloaded, err := store.GetIssue(ctx, "exec-1", issueID)
		if err != nil {
			t.Fatalf("GetIssue(%s): %v", issueID, err)
		}
		if reloaded.State != domain.StateReady {
			t.Fatalf("persisted state for %s = %s, want READY", issueID, reloaded.State)
		}
		if reloaded.ProviderLimitRetryAt != nil {
			t.Fatalf("ProviderLimitRetryAt for %s = %v, want nil after retry", issueID, reloaded.ProviderLimitRetryAt)
		}
	}
	if len(resumer.calls) != 1 || resumer.calls[0] != "exec-1" {
		t.Fatalf("expected one redispatch of exec-1, got %+v", resumer.calls)
	}
}

// TestProviderLimitControllerLeavesNotYetDueIssueParked proves the backoff is
// honoured: an Issue whose deadline is still ahead is not touched.
func TestProviderLimitControllerLeavesNotYetDueIssueParked(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Minute)
	seedParkedProviderLimitIssue(t, store, "exec-1", "issue-1", 3, 1, &future)

	resumer := &fakeExecutionResumer{}
	controller := engine.NewProviderLimitController(store, resumer, func() time.Time { return now })

	results, err := controller.ReconcileOnce(ctx)
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %+v", results)
	}
	reloaded, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if reloaded.State != domain.StateProviderLimit {
		t.Fatalf("state = %s, want PROVIDER_LIMIT", reloaded.State)
	}
	if reloaded.ProviderLimitRetryAt == nil {
		t.Fatalf("ProviderLimitRetryAt was cleared for an Issue that is not yet due")
	}
	if len(resumer.calls) != 0 {
		t.Fatalf("expected no redispatch, got %+v", resumer.calls)
	}
}

// TestProviderLimitControllerFailsDueIssueWithExhaustedBudget proves the
// bounded budget also holds at the controller: a due Issue with no room left
// moves to FAILED instead of READY, and its Execution is not redispatched.
func TestProviderLimitControllerFailsDueIssueWithExhaustedBudget(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	seedParkedProviderLimitIssue(t, store, "exec-1", "issue-1", 2, 2, &past)

	resumer := &fakeExecutionResumer{}
	controller := engine.NewProviderLimitController(store, resumer, func() time.Time { return now })

	results, err := controller.ReconcileOnce(ctx)
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %+v", results)
	}
	if results[0].Retried {
		t.Fatalf("expected no retry for an exhausted budget, got %+v", results[0])
	}
	if !results[0].Exhausted {
		t.Fatalf("expected the result to report an exhausted budget, got %+v", results[0])
	}
	reloaded, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if reloaded.State != domain.StateFailed {
		t.Fatalf("state = %s, want FAILED", reloaded.State)
	}
	if reloaded.ProviderLimitRetryAt != nil {
		t.Fatalf("ProviderLimitRetryAt = %v, want nil after the budget is exhausted", reloaded.ProviderLimitRetryAt)
	}
	if len(resumer.calls) != 0 {
		t.Fatalf("expected no redispatch, got %+v", resumer.calls)
	}
}

// TestProviderLimitControllerContinuesAfterResumerError proves one bad
// Execution never stops the rest of a pass, matching LostExecutionController.
func TestProviderLimitControllerContinuesAfterResumerError(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	seedParkedProviderLimitIssue(t, store, "exec-1", "issue-1", 3, 1, &past)
	seedParkedProviderLimitIssue(t, store, "exec-2", "issue-2", 3, 1, &past)

	resumeErr := errors.New("resume boom")
	resumer := &fakeExecutionResumer{err: resumeErr}
	controller := engine.NewProviderLimitController(store, resumer, func() time.Time { return now })

	results, err := controller.ReconcileOnce(ctx)
	if err == nil {
		t.Fatalf("expected an aggregated error from the failing resumer")
	}
	if !errors.Is(err, resumeErr) {
		t.Fatalf("expected wrapped resumeErr, got %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected both issues to still be retried, got %+v", results)
	}
	if len(resumer.calls) != 2 {
		t.Fatalf("expected a redispatch attempt per execution, got %+v", resumer.calls)
	}
}

// TestResumeExecutionLeavesParkedProviderLimitIssueAlone proves `forge
// resume` does not re-enter Prepare/Execute for an Issue still waiting out
// its backoff. ProviderLimitController owns that exit.
func TestResumeExecutionLeavesParkedProviderLimitIssueAlone(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{"issue-1": {ID: "issue-1"}})
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Minute)
	seedParkedProviderLimitIssue(t, te.store, "exec-1", "issue-1", 3, 1, &future)

	state, err := te.eng.ResumeExecution(ctx, "exec-1")
	if err != nil {
		t.Fatalf("ResumeExecution: %v", err)
	}
	if len(state.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %+v", state.Issues)
	}
	if state.Issues[0].State != domain.StateProviderLimit {
		t.Fatalf("state = %s, want PROVIDER_LIMIT", state.Issues[0].State)
	}
	if len(te.fake.Invocations()) != 0 {
		t.Fatalf("expected no Agent invocation for a parked issue, got %+v", te.fake.Invocations())
	}
}

// TestProviderLimitControllerRunReconcilesUntilContextCancelled mirrors
// LostExecutionController's Run test: Run loops until ctx ends.
func TestProviderLimitControllerRunReconcilesUntilContextCancelled(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	seedParkedProviderLimitIssue(t, store, "exec-1", "issue-1", 3, 1, &past)

	resumer := &fakeExecutionResumer{}
	controller := engine.NewProviderLimitController(store, resumer, func() time.Time { return now })

	sleepCalls := 0
	controller.Sleep = func(ctx context.Context, _ time.Duration) error {
		sleepCalls++
		if sleepCalls >= 2 {
			cancel()
		}
		return ctx.Err()
	}

	if err := controller.Run(ctx, time.Millisecond, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if sleepCalls != 2 {
		t.Fatalf("expected 2 Sleep calls, got %d", sleepCalls)
	}
	// The first pass retried the Issue and cleared its deadline, so the
	// second pass finds nothing due.
	if len(resumer.calls) != 1 || resumer.calls[0] != "exec-1" {
		t.Fatalf("expected exactly one redispatch, got %+v", resumer.calls)
	}
}

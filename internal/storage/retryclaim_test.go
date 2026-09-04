package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

// seedFailedIssue creates an Execution with one FAILED Issue that has an
// exhausted gate budget and an active Worker claim.
func seedFailedIssue(t *testing.T, store *storage.SQLiteStore, executionID, issueID string) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{ID: executionID, BaseRevision: "base", StartedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if err := store.CreateIssue(ctx, domain.Issue{
		ID: issueID, ExecutionID: executionID, State: domain.StateFailed, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudgetFrom(domain.RetryLimits{Gate: 2, Review: 2, CI: 2}, 2, 0, 0, 0),
	}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.ClaimIssue(ctx, executionID, issueID, "worker-old"); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
}

// TestClaimRetry_ResetsBudgetReleasesClaimAndTransitions proves the winning
// caller gets one atomic retry claim.
func TestClaimRetry_ResetsBudgetReleasesClaimAndTransitions(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedFailedIssue(t, store, "exec-1", "issue-1")

	claim, err := store.ClaimRetry(ctx, "exec-1", "issue-1", domain.NewRetryBudget(domain.RetryLimits{Gate: 2, Review: 2, CI: 2}))
	if err != nil {
		t.Fatalf("ClaimRetry: %v", err)
	}
	if claim.From != domain.StateFailed {
		t.Fatalf("claim from = %s, want FAILED", claim.From)
	}
	if claim.Issue.State != domain.StateReady {
		t.Fatalf("returned state = %s, want READY", claim.Issue.State)
	}
	if claim.Issue.RetryBudget.GateFailures() != 0 {
		t.Fatalf("returned gate failures = %d, want 0", claim.Issue.RetryBudget.GateFailures())
	}

	got, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.State != domain.StateReady {
		t.Fatalf("persisted state = %s, want READY", got.State)
	}
	if got.RetryBudget.GateFailures() != 0 {
		t.Fatalf("gate failures = %d, want 0", got.RetryBudget.GateFailures())
	}
	if _, err := store.WorkerClaim(ctx, "exec-1", "issue-1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("WorkerClaim error = %v, want ErrNotFound", err)
	}

	events, err := store.EventsByIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	var sawTransition bool
	for _, e := range events {
		if e.Type == "issue.transitioned" {
			sawTransition = true
		}
	}
	if !sawTransition {
		t.Fatalf("no issue.transitioned event recorded; events=%+v", events)
	}
}

// TestClaimRetry_LoserChangesNothing proves a second concurrent retry loses
// the CAS and leaves the winner's fresh claim and budget alone.
func TestClaimRetry_LoserChangesNothing(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedFailedIssue(t, store, "exec-1", "issue-1")
	limits := domain.RetryLimits{Gate: 2, Review: 2, CI: 2}

	if _, err := store.ClaimRetry(ctx, "exec-1", "issue-1", domain.NewRetryBudget(limits)); err != nil {
		t.Fatalf("first ClaimRetry: %v", err)
	}
	// The winner proceeds: it re-claims the Issue and starts working.
	if err := store.ClaimIssue(ctx, "exec-1", "issue-1", "worker-new"); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
	if _, err := store.TransitionIssue(ctx, "exec-1", "issue-1", domain.StateClaimed); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}

	_, err := store.ClaimRetry(ctx, "exec-1", "issue-1", domain.NewRetryBudget(limits))
	if !errors.Is(err, storage.ErrConcurrentModification) {
		t.Fatalf("second ClaimRetry error = %v, want ErrConcurrentModification", err)
	}
	var conflict *storage.RetryClaimConflictError
	if !errors.As(err, &conflict) || conflict.State != domain.StateClaimed {
		t.Fatalf("conflict = %+v, want the observed CLAIMED state", conflict)
	}

	claim, err := store.WorkerClaim(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("WorkerClaim: %v", err)
	}
	if claim.WorkerRef != "worker-new" {
		t.Fatalf("claim worker ref = %s, want worker-new", claim.WorkerRef)
	}
	got, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.State != domain.StateClaimed {
		t.Fatalf("persisted state = %s, want CLAIMED", got.State)
	}
}

// TestClaimRetry_ReportsTheObservedState proves the conflict names the state
// it found, so a caller can tell a rival retry from a concurrent cancel.
func TestClaimRetry_ReportsTheObservedState(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedFailedIssue(t, store, "exec-1", "issue-1")
	// A concurrent cancel moves the Issue out of FAILED before the claim.
	if _, err := store.ClaimRetry(ctx, "exec-1", "issue-1", domain.NewRetryBudget(domain.RetryLimits{Gate: 2})); err != nil {
		t.Fatalf("first ClaimRetry: %v", err)
	}
	if _, err := store.TransitionIssue(ctx, "exec-1", "issue-1", domain.StateCancelled); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}

	_, err := store.ClaimRetry(ctx, "exec-1", "issue-1", domain.NewRetryBudget(domain.RetryLimits{Gate: 2}))
	var conflict *storage.RetryClaimConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("ClaimRetry error = %v (%T), want *RetryClaimConflictError", err, err)
	}
	if conflict.State != domain.StateCancelled {
		t.Fatalf("conflict state = %s, want CANCELLED", conflict.State)
	}
	if !errors.Is(err, storage.ErrConcurrentModification) {
		t.Fatalf("conflict does not wrap ErrConcurrentModification: %v", err)
	}
}

// TestAbortRetry_RestoresFailedStateAndBudget proves the compensating write
// undoes a claim the caller could not complete.
func TestAbortRetry_RestoresFailedStateAndBudget(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedFailedIssue(t, store, "exec-1", "issue-1")
	limits := domain.RetryLimits{Gate: 2, Review: 2, CI: 2}
	before := domain.NewRetryBudgetFrom(limits, 2, 0, 0, 0)

	if _, err := store.ClaimRetry(ctx, "exec-1", "issue-1", domain.NewRetryBudget(limits)); err != nil {
		t.Fatalf("ClaimRetry: %v", err)
	}
	if err := store.AbortRetry(ctx, "exec-1", "issue-1", before); err != nil {
		t.Fatalf("AbortRetry: %v", err)
	}

	got, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.State != domain.StateFailed {
		t.Fatalf("state = %s, want FAILED", got.State)
	}
	if got.RetryBudget.GateFailures() != 2 {
		t.Fatalf("gate failures = %d, want the pre-claim 2", got.RetryBudget.GateFailures())
	}
}

// TestAbortRetry_RefusesWhenTheIssueLeftReady proves the rollback never
// overwrites a state some other actor set after the claim.
func TestAbortRetry_RefusesWhenTheIssueLeftReady(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedFailedIssue(t, store, "exec-1", "issue-1")
	limits := domain.RetryLimits{Gate: 2}

	if _, err := store.ClaimRetry(ctx, "exec-1", "issue-1", domain.NewRetryBudget(limits)); err != nil {
		t.Fatalf("ClaimRetry: %v", err)
	}
	if _, err := store.TransitionIssue(ctx, "exec-1", "issue-1", domain.StateCancelled); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}

	err := store.AbortRetry(ctx, "exec-1", "issue-1", domain.NewRetryBudgetFrom(limits, 2, 0, 0, 0))
	if !errors.Is(err, storage.ErrConcurrentModification) {
		t.Fatalf("AbortRetry error = %v, want ErrConcurrentModification", err)
	}
	got, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.State != domain.StateCancelled {
		t.Fatalf("state = %s, want CANCELLED left alone", got.State)
	}
}

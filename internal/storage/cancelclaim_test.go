package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

// seedClaimedIssue creates an Execution with one Issue in state and an
// active Worker claim, the shape CancelExecution acts on.
func seedClaimedIssue(t *testing.T, store *storage.SQLiteStore, executionID, issueID string, state domain.IssueState) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{ID: executionID, BaseRevision: "base", StartedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if err := store.CreateIssue(ctx, domain.Issue{
		ID: issueID, ExecutionID: executionID, State: state, Scope: domain.ScopeManaged,
	}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.ClaimIssue(ctx, executionID, issueID, "worker-1"); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
}

// TestClaimCancel_TransitionsAndReleasesClaim proves ClaimCancel applies the
// CANCELLED transition and releases the Worker claim as one transaction.
func TestClaimCancel_TransitionsAndReleasesClaim(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedClaimedIssue(t, store, "exec-1", "issue-1", domain.StateImplementing)

	issue, err := store.ClaimCancel(ctx, "exec-1", "issue-1", domain.StateImplementing, true)
	if err != nil {
		t.Fatalf("ClaimCancel: %v", err)
	}
	if issue.State != domain.StateCancelled {
		t.Fatalf("returned state = %s, want CANCELLED", issue.State)
	}

	got, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.State != domain.StateCancelled {
		t.Fatalf("persisted state = %s, want CANCELLED", got.State)
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

// TestClaimCancel_KeepsClaimWhenNotReleasing proves a caller can cancel an
// Issue whose owner is still running without letting go of its Worker
// claim, so a second Execution cannot grab it while the live owner still
// writes to it.
func TestClaimCancel_KeepsClaimWhenNotReleasing(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedClaimedIssue(t, store, "exec-1", "issue-1", domain.StateImplementing)

	if _, err := store.ClaimCancel(ctx, "exec-1", "issue-1", domain.StateImplementing, false); err != nil {
		t.Fatalf("ClaimCancel: %v", err)
	}

	got, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.State != domain.StateCancelled {
		t.Fatalf("persisted state = %s, want CANCELLED", got.State)
	}
	claim, err := store.WorkerClaim(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("WorkerClaim: %v, want the claim kept", err)
	}
	if claim.WorkerRef != "worker-1" {
		t.Fatalf("claim worker ref = %s, want worker-1", claim.WorkerRef)
	}
}

// TestClaimCancel_ReportsConflictWhenStateMoved proves a cancel that CASes
// off a stale observed state loses cleanly, and names the state it found,
// instead of silently overwriting whatever another actor did.
func TestClaimCancel_ReportsConflictWhenStateMoved(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedClaimedIssue(t, store, "exec-1", "issue-1", domain.StateImplementing)

	if _, err := store.TransitionIssue(ctx, "exec-1", "issue-1", domain.StateValidating); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}

	_, err := store.ClaimCancel(ctx, "exec-1", "issue-1", domain.StateImplementing, true)
	if !errors.Is(err, storage.ErrConcurrentModification) {
		t.Fatalf("ClaimCancel error = %v, want ErrConcurrentModification", err)
	}
	var conflict *storage.CancelClaimConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("ClaimCancel error = %v (%T), want *CancelClaimConflictError", err, err)
	}
	if conflict.State != domain.StateValidating {
		t.Fatalf("conflict state = %s, want VALIDATING", conflict.State)
	}

	got, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.State != domain.StateValidating {
		t.Fatalf("persisted state = %s, want VALIDATING (unchanged)", got.State)
	}
	if _, err := store.WorkerClaim(ctx, "exec-1", "issue-1"); err != nil {
		t.Fatalf("WorkerClaim = %v, want the claim untouched", err)
	}
}

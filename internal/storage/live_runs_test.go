package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

// TestLiveRuns_EnumeratesRunsAcrossPhases covers the phase-agnostic
// live-runs enumerator a live reader uses to find which AgentRuns to tail
// (ADR 0030): it returns every run's id together with its owning Execution
// and Issue, regardless of phase (IMPLEMENTING, REVIEWING, planning).
func TestLiveRuns_EnumeratesRunsAcrossPhases(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// Two phase-agnostic runs on different issues of one Execution.
	seedIssueForAgentRun(t, store, "exec-live", "issue-a")
	// Create a second issue under the same Execution without re-making it.
	if err := store.CreateIssue(ctx, domain.Issue{
		ID: "issue-b", ExecutionID: "exec-live", State: domain.StateImplementing, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
	}); err != nil {
		t.Fatalf("CreateIssue(b): %v", err)
	}

	aID, err := store.RecordAgentRun(ctx, storage.AgentRun{
		ExecutionID: "exec-live", IssueID: "issue-a",
		Backend: "claude-code", StartedAt: time.Now(), FinishedAt: time.Now(), Result: "IMPLEMENTED",
	})
	if err != nil {
		t.Fatalf("RecordAgentRun(a): %v", err)
	}
	bID, err := store.RecordAgentRun(ctx, storage.AgentRun{
		ExecutionID: "exec-live", IssueID: "issue-b",
		Backend: "claude-code", StartedAt: time.Now(), FinishedAt: time.Now(), Result: "IMPLEMENTED",
	})
	if err != nil {
		t.Fatalf("RecordAgentRun(b): %v", err)
	}

	runs, err := store.LiveRuns(ctx)
	if err != nil {
		t.Fatalf("LiveRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2: %+v", len(runs), runs)
	}

	byID := map[int64]storage.LiveRun{}
	for _, r := range runs {
		byID[r.AgentRunID] = r
	}
	for runID, wantIssue := range map[int64]string{aID: "issue-a", bID: "issue-b"} {
		r, ok := byID[runID]
		if !ok {
			t.Fatalf("live runs missing run id %d: %+v", runID, runs)
		}
		if r.ExecutionID != "exec-live" || r.IssueID != wantIssue {
			t.Fatalf("run %d = %+v, want execution exec-live issue %s", runID, r, wantIssue)
		}
	}
}

// TestLiveRuns_EmptyReturnsNoRuns covers the empty case so a live reader
// never sees a nil that sprays errors.
func TestLiveRuns_EmptyReturnsNoRuns(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	runs, err := store.LiveRuns(ctx)
	if err != nil {
		t.Fatalf("LiveRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("got %d runs for empty store, want 0", len(runs))
	}
}

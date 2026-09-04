package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
)

// TestListActiveExecutions_BucketsIssueStatesByCanonicalGroup pins that the
// active-execution summary counts issues by domain.IssueState.Group(), the
// canonical coarse grouping, and never by a private switch. Each canonical
// bucket is exercised: GroupFailed -> FailedIssues, the DONE and CANCELLED
// halves of GroupDone split into DoneIssues/CancelledIssues, and every
// non-terminal bucket -> ActiveIssues.
func TestListActiveExecutions_BucketsIssueStatesByCanonicalGroup(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	started := time.Unix(0, 0).UTC()
	cfg := config.Default().Retry

	for _, execID := range []string{"exec", "exec-all-done"} {
		if err := store.CreateExecution(ctx, domain.Execution{ID: execID, StartedAt: started}); err != nil {
			t.Fatalf("CreateExecution %s: %v", execID, err)
		}
	}

	// One issue in every canonical group so no bucket silently maps to the
	// default active branch.
	issues := []struct {
		id    string
		state domain.IssueState
	}{
		{"pending", domain.StatePending},
		{"working", domain.StateImplementing},
		{"waiting", domain.StateCIPending},
		{"blocked", domain.StateProviderLimit},
		{"failed", domain.StateFailed},
		{"done", domain.StateDone},
		{"cancelled", domain.StateCancelled},
	}
	for _, is := range issues {
		if err := store.CreateIssue(ctx, domain.Issue{
			ID: is.id, ExecutionID: "exec", State: is.state,
			Scope: domain.ScopeManaged, RetryBudget: domain.NewRetryBudget(cfg),
		}); err != nil {
			t.Fatalf("CreateIssue %s: %v", is.id, err)
		}
	}

	summaries, err := engine.ListActiveExecutions(ctx, store)
	if err != nil {
		t.Fatalf("ListActiveExecutions: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("len(summaries) = %d, want 1 (only the active execution is listed)", len(summaries))
	}

	got := summaries[0]
	if got.IssueCount != 7 {
		t.Errorf("IssueCount = %d, want 7", got.IssueCount)
	}
	if got.ActiveIssues != 4 {
		t.Errorf("ActiveIssues = %d, want 4 (pending+working+waiting+blocked)", got.ActiveIssues)
	}
	if got.DoneIssues != 1 {
		t.Errorf("DoneIssues = %d, want 1", got.DoneIssues)
	}
	if got.FailedIssues != 1 {
		t.Errorf("FailedIssues = %d, want 1", got.FailedIssues)
	}
	if got.CancelledIssues != 1 {
		t.Errorf("CancelledIssues = %d, want 1", got.CancelledIssues)
	}
}

// TestListActiveExecutions_FiltersExecutionsWithNoActiveIssues asserts an
// execution made up entirely of terminal (GroupDone) issues is not reported
// as an active execution.
func TestListActiveExecutions_FiltersExecutionsWithNoActiveIssues(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	cfg := config.Default().Retry

	if err := store.CreateExecution(ctx, domain.Execution{ID: "exec-all-done", StartedAt: time.Unix(0, 0).UTC()}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	for _, st := range []domain.IssueState{domain.StateDone, domain.StateCancelled, domain.StateFailed} {
		if err := store.CreateIssue(ctx, domain.Issue{
			ID: string(st), ExecutionID: "exec-all-done", State: st,
			Scope: domain.ScopeManaged, RetryBudget: domain.NewRetryBudget(cfg),
		}); err != nil {
			t.Fatalf("CreateIssue %s: %v", st, err)
		}
	}

	summaries, err := engine.ListActiveExecutions(ctx, store)
	if err != nil {
		t.Fatalf("ListActiveExecutions: %v", err)
	}
	if len(summaries) != 0 {
		t.Errorf("len(summaries) = %d, want 0", len(summaries))
	}
}

package domain_test

import (
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
)

// TestDomainTypesConstruct is a smoke test that exercises the core domain
// types together, ensuring the shapes described in CONTEXT.md compose.
func TestDomainTypesConstruct(t *testing.T) {
	exec := domain.Execution{
		ID:           "exec-1",
		BaseRevision: "abc123",
		StartedAt:    time.Now(),
	}

	dep := domain.Dependency{
		IssueID:     "345",
		DependsOnID: "344",
	}

	issue := domain.Issue{
		ID:           "345",
		ExecutionID:  exec.ID,
		State:        domain.StatePending,
		Scope:        domain.ScopeManaged,
		Dependencies: []domain.Dependency{dep},
		RetryBudget:  domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 2, CI: 3}),
	}

	ws := domain.Workspace{
		IssueID: issue.ID,
		Path:    "/tmp/forge/worktrees/345",
		Branch:  "agent/345",
	}

	worker := domain.Worker{
		IssueID:     issue.ID,
		ExecutionID: exec.ID,
		Workspace:   ws,
	}

	if issue.Dependencies[0].IssueID != "345" || issue.Dependencies[0].DependsOnID != "344" {
		t.Fatalf("dependency not wired correctly: %+v", issue.Dependencies[0])
	}
	if worker.Workspace.Branch != "agent/345" {
		t.Fatalf("worker workspace not wired correctly: %+v", worker.Workspace)
	}
	if issue.RetryBudget.RemainingGate() != 3 {
		t.Fatalf("expected fresh retry budget, got %+v", issue.RetryBudget)
	}
}

// TestIssueRecordGateFailurePersistsThroughPointer proves that recording a
// retry failure via the Issue-level wrapper methods mutates the Issue's
// embedded RetryBudget in place — including when the Issue is reached
// through a slice/range copy, which is exactly how a scheduler loop would
// hold it. A value-receiver path here would silently discard the increment.
func TestIssueRecordGateFailurePersistsThroughPointer(t *testing.T) {
	issues := []domain.Issue{
		{ID: "345", RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 2, CI: 3})},
	}

	for i := range issues {
		if err := issues[i].RecordGateFailure(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if got := issues[0].RetryBudget.RemainingGate(); got != 2 {
		t.Fatalf("expected gate failure to persist on the Issue, remaining gate = %d, want 2", got)
	}

	if err := issues[0].RecordReviewRejection(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := issues[0].RecordCIFailure(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := issues[0].RetryBudget.RemainingReview(); got != 1 {
		t.Fatalf("expected review rejection to persist on the Issue, remaining review = %d, want 1", got)
	}
	if got := issues[0].RetryBudget.RemainingCI(); got != 2 {
		t.Fatalf("expected CI failure to persist on the Issue, remaining CI = %d, want 2", got)
	}
}

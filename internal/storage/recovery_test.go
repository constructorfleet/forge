package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

func seedRecoveryIssue(t *testing.T, store *storage.SQLiteStore, executionID, issueID string, state domain.IssueState) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{
		ID:           executionID,
		BaseRevision: "base-sha",
		StartedAt:    time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if err := store.CreateIssue(ctx, domain.Issue{
		ID:          issueID,
		ExecutionID: executionID,
		Title:       "Issue " + issueID,
		State:       state,
		Scope:       domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
	}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
}

func TestRecordWorkspace_PersistsAndReloads(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedRecoveryIssue(t, store, "exec-ws", "issue-ws", domain.StatePreparing)

	want := domain.Workspace{
		IssueID: "issue-ws",
		Path:    "/tmp/forge/worktrees/exec-ws/issue-ws",
		Branch:  "forge/exec-ws/issue-ws",
	}
	if err := store.RecordWorkspace(ctx, "exec-ws", want); err != nil {
		t.Fatalf("RecordWorkspace: %v", err)
	}

	got, err := store.WorkspaceByIssue(ctx, "exec-ws", "issue-ws")
	if err != nil {
		t.Fatalf("WorkspaceByIssue: %v", err)
	}
	if got != want {
		t.Fatalf("workspace = %+v, want %+v", got, want)
	}
}

func TestWorkerClaimOwnerAndRelease_AllowsReclaim(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedRecoveryIssue(t, store, "exec-worker", "issue-worker", domain.StateClaimed)

	if err := store.ClaimIssue(ctx, "exec-worker", "issue-worker", "worker-a"); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
	if err := store.UpdateWorkerOwner(ctx, "exec-worker", "issue-worker", 4242); err != nil {
		t.Fatalf("UpdateWorkerOwner: %v", err)
	}

	claim, err := store.WorkerClaim(ctx, "exec-worker", "issue-worker")
	if err != nil {
		t.Fatalf("WorkerClaim: %v", err)
	}
	if claim.WorkerRef != "worker-a" || claim.OwnerPID != 4242 {
		t.Fatalf("claim = %+v, want worker-a owned by 4242", claim)
	}

	if err := store.ReleaseWorkerClaim(ctx, "exec-worker", "issue-worker"); err != nil {
		t.Fatalf("ReleaseWorkerClaim: %v", err)
	}
	if _, err := store.WorkerClaim(ctx, "exec-worker", "issue-worker"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("WorkerClaim after release = %v, want ErrNotFound", err)
	}

	if err := store.ClaimIssue(ctx, "exec-worker", "issue-worker", "worker-b"); err != nil {
		t.Fatalf("reclaim ClaimIssue: %v", err)
	}
}

func TestRecordPullRequest_IdenticalRecoveryIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForPullRequest(t, store, "exec-pr-idem", "issue-pr-idem")

	pr := storage.PullRequest{
		ExecutionID: "exec-pr-idem",
		IssueID:     "issue-pr-idem",
		Number:      99,
		URL:         "https://example.invalid/pr/99",
		CommitSHA:   "deadbeef",
		CreatedAt:   time.Date(2026, 8, 28, 12, 1, 0, 0, time.UTC),
	}
	if err := store.RecordPullRequest(ctx, pr); err != nil {
		t.Fatalf("first RecordPullRequest: %v", err)
	}
	if err := store.RecordPullRequest(ctx, pr); err != nil {
		t.Fatalf("second RecordPullRequest: %v", err)
	}

	prs, err := store.PullRequestsByIssue(ctx, "exec-pr-idem", "issue-pr-idem")
	if err != nil {
		t.Fatalf("PullRequestsByIssue: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("got %d pull requests, want 1 after identical recovery", len(prs))
	}

	events, err := store.EventsByIssue(ctx, "exec-pr-idem", "issue-pr-idem")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1 after identical recovery", len(events))
	}
}

func TestRecordPullRequest_RecoveryFillsMissingBaseBranch(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForPullRequest(t, store, "exec-pr-base-recovery", "issue-pr-base-recovery")

	pr := storage.PullRequest{
		ExecutionID: "exec-pr-base-recovery",
		IssueID:     "issue-pr-base-recovery",
		Number:      99,
		URL:         "https://example.invalid/pr/99",
		CommitSHA:   "deadbeef",
		CreatedAt:   time.Date(2026, 8, 28, 12, 1, 0, 0, time.UTC),
	}
	if err := store.RecordPullRequest(ctx, pr); err != nil {
		t.Fatalf("first RecordPullRequest: %v", err)
	}
	pr.BaseBranch = "main"
	if err := store.RecordPullRequest(ctx, pr); err != nil {
		t.Fatalf("second RecordPullRequest: %v", err)
	}

	prs, err := store.PullRequestsByIssue(ctx, "exec-pr-base-recovery", "issue-pr-base-recovery")
	if err != nil {
		t.Fatalf("PullRequestsByIssue: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("got %d pull requests, want 1 after base branch recovery", len(prs))
	}
	if prs[0].BaseBranch != "main" {
		t.Fatalf("BaseBranch = %q, want main", prs[0].BaseBranch)
	}
}

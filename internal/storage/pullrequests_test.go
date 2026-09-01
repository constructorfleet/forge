package storage_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

func seedIssueForPullRequest(t *testing.T, store *storage.SQLiteStore, executionID, issueID string) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{ID: executionID, BaseRevision: "abc123", StartedAt: time.Now()}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	issue := domain.Issue{
		ID: issueID, ExecutionID: executionID, Title: "Ship it",
		State: domain.StatePending, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
}

func TestRecordPullRequest_PersistsAndAppendsEvent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForPullRequest(t, store, "exec-pr", "issue-pr")

	createdAt := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	pr := storage.PullRequest{
		ExecutionID: "exec-pr",
		IssueID:     "issue-pr",
		Number:      22,
		URL:         "https://example.invalid/pr/22",
		BaseBranch:  "main",
		CommitSHA:   "deadbeef",
		CreatedAt:   createdAt,
	}
	if err := store.RecordPullRequest(ctx, pr); err != nil {
		t.Fatalf("RecordPullRequest: %v", err)
	}

	prs, err := store.PullRequestsByIssue(ctx, "exec-pr", "issue-pr")
	if err != nil {
		t.Fatalf("PullRequestsByIssue: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("got %d pull requests, want 1", len(prs))
	}
	got := prs[0]
	if got.Number != 22 || got.URL != "https://example.invalid/pr/22" || got.BaseBranch != "main" || got.CommitSHA != "deadbeef" {
		t.Fatalf("persisted pull request = %+v", got)
	}
	if !got.CreatedAt.Equal(createdAt) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, createdAt)
	}

	events, err := store.EventsByIssue(ctx, "exec-pr", "issue-pr")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	if len(events) != 1 || events[0].Type != "pull_request.created" {
		t.Fatalf("events = %+v, want one pull_request.created event", events)
	}
	var payload struct {
		URL        string `json:"url"`
		Number     int    `json:"number"`
		BaseBranch string `json:"base_branch"`
		CommitSHA  string `json:"commit_sha"`
	}
	if err := json.Unmarshal([]byte(events[0].Data), &payload); err != nil {
		t.Fatalf("unmarshal pull_request.created event: %v", err)
	}
	if payload.URL != pr.URL || payload.Number != pr.Number || payload.BaseBranch != pr.BaseBranch || payload.CommitSHA != pr.CommitSHA {
		t.Fatalf("event payload = %+v, want %+v", payload, pr)
	}
}

func TestPullRequestsByIssue_ReturnsEmptyWhenNoneRecorded(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForPullRequest(t, store, "exec-pr-empty", "issue-pr-empty")

	prs, err := store.PullRequestsByIssue(ctx, "exec-pr-empty", "issue-pr-empty")
	if err != nil {
		t.Fatalf("PullRequestsByIssue: %v", err)
	}
	if len(prs) != 0 {
		t.Fatalf("got %d pull requests, want 0", len(prs))
	}
}

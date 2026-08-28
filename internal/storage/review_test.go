package storage_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

func seedIssueForReviewRun(t *testing.T, store *storage.SQLiteStore, executionID, issueID string) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{ID: executionID, BaseRevision: "abc123", StartedAt: time.Now()}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	issue := domain.Issue{
		ID: issueID, ExecutionID: executionID,
		State: domain.StatePending, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
}

func TestRecordReviewRun_ApprovedPersistsWithNoFindings(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForReviewRun(t, store, "exec-review", "issue-review")

	started := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	finished := started.Add(3 * time.Second)
	run := storage.ReviewRun{
		ExecutionID: "exec-review",
		IssueID:     "issue-review",
		Verdict:     "APPROVED",
		Summary:     "looks good",
		Diff:        "diff --git a b",
		StartedAt:   started,
		FinishedAt:  finished,
	}
	if err := store.RecordReviewRun(ctx, run); err != nil {
		t.Fatalf("RecordReviewRun: %v", err)
	}

	runs, err := store.ReviewRunsByIssue(ctx, "exec-review", "issue-review")
	if err != nil {
		t.Fatalf("ReviewRunsByIssue: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d review runs, want 1", len(runs))
	}
	got := runs[0]
	if got.Verdict != "APPROVED" || got.Summary != "looks good" || got.Diff != "diff --git a b" {
		t.Errorf("got = %+v, want Verdict APPROVED Summary %q Diff %q", got, "looks good", "diff --git a b")
	}
	if len(got.Findings) != 0 {
		t.Errorf("got %d findings, want 0", len(got.Findings))
	}
	if !got.StartedAt.Equal(started) || !got.FinishedAt.Equal(finished) {
		t.Errorf("StartedAt/FinishedAt = %v/%v, want %v/%v", got.StartedAt, got.FinishedAt, started, finished)
	}
}

func TestRecordReviewRun_ChangesRequiredPersistsFindings(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForReviewRun(t, store, "exec-review-2", "issue-review-2")

	run := storage.ReviewRun{
		ExecutionID: "exec-review-2",
		IssueID:     "issue-review-2",
		Verdict:     "CHANGES_REQUIRED",
		Summary:     "one blocking issue",
		StartedAt:   time.Now(),
		FinishedAt:  time.Now(),
		Findings: []storage.ReviewFinding{
			{Severity: "ERROR", File: "main.go", Line: 42, Message: "unhandled error"},
			{Severity: "WARNING", Message: "consider simplifying"},
		},
	}
	if err := store.RecordReviewRun(ctx, run); err != nil {
		t.Fatalf("RecordReviewRun: %v", err)
	}

	runs, err := store.ReviewRunsByIssue(ctx, "exec-review-2", "issue-review-2")
	if err != nil {
		t.Fatalf("ReviewRunsByIssue: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d review runs, want 1", len(runs))
	}
	findings := runs[0].Findings
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
	}
	if findings[0].Severity != "ERROR" || findings[0].File != "main.go" || findings[0].Line != 42 || findings[0].Message != "unhandled error" {
		t.Errorf("findings[0] = %+v, want ERROR main.go:42 %q", findings[0], "unhandled error")
	}
	if findings[1].Severity != "WARNING" || findings[1].File != "" || findings[1].Line != 0 || findings[1].Message != "consider simplifying" {
		t.Errorf("findings[1] = %+v, want WARNING with no anchored location and %q", findings[1], "consider simplifying")
	}
}

func TestRecordReviewRun_AppendsReviewRunEvent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForReviewRun(t, store, "exec-review-3", "issue-review-3")

	run := storage.ReviewRun{
		ExecutionID: "exec-review-3",
		IssueID:     "issue-review-3",
		Verdict:     "CHANGES_REQUIRED",
		Summary:     "needs work",
		StartedAt:   time.Now(),
		FinishedAt:  time.Now(),
		Findings: []storage.ReviewFinding{
			{Severity: "ERROR", Message: "bug"},
		},
	}
	if err := store.RecordReviewRun(ctx, run); err != nil {
		t.Fatalf("RecordReviewRun: %v", err)
	}

	events, err := store.EventsByIssue(ctx, "exec-review-3", "issue-review-3")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	var reviewEvent *storage.Event
	for i := range events {
		if events[i].Type == "review.run" {
			reviewEvent = &events[i]
		}
	}
	if reviewEvent == nil {
		t.Fatalf("no review.run event found among %+v", events)
	}
	var payload struct {
		Verdict      string `json:"verdict"`
		Summary      string `json:"summary"`
		FindingCount int    `json:"finding_count"`
	}
	if err := json.Unmarshal([]byte(reviewEvent.Data), &payload); err != nil {
		t.Fatalf("unmarshal review.run event data: %v", err)
	}
	if payload.Verdict != "CHANGES_REQUIRED" || payload.Summary != "needs work" || payload.FindingCount != 1 {
		t.Errorf("payload = %+v, want Verdict CHANGES_REQUIRED Summary %q FindingCount 1", payload, "needs work")
	}
}

func TestReviewRunsByIssue_ReturnsEmptyForIssueWithNoReviewRuns(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForReviewRun(t, store, "exec-review-4", "issue-review-4")

	runs, err := store.ReviewRunsByIssue(ctx, "exec-review-4", "issue-review-4")
	if err != nil {
		t.Fatalf("ReviewRunsByIssue: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("got %d review runs, want 0", len(runs))
	}
}

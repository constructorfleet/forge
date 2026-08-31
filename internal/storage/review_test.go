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

// TestRecordReviewRun_PersistsAxisEnvelopesRoundTrip is issue #162's core
// storage acceptance criterion (widened by issue #182 to cover
// assurances): a ReviewRun's per-axis raw envelopes (coverage, token usage,
// raw findings+assurances JSON) round-trip through
// RecordReviewRun/ReviewRunsByIssue exactly as given, so a past Review is
// fully reconstructable.
func TestRecordReviewRun_PersistsAxisEnvelopesRoundTrip(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForReviewRun(t, store, "exec-review-5", "issue-review-5")

	inTokens, outTokens := 120, 340
	run := storage.ReviewRun{
		ExecutionID: "exec-review-5",
		IssueID:     "issue-review-5",
		Verdict:     "CHANGES_REQUIRED",
		Summary:     "axes bugs+quality+docs: 1 finding(s), verdict CHANGES_REQUIRED",
		Diff:        "diff --git a b",
		StartedAt:   time.Now(),
		FinishedAt:  time.Now(),
		Findings: []storage.ReviewFinding{
			{Severity: "ERROR", File: "main.go", Line: 42, Message: "unhandled error"},
		},
		Envelopes: []storage.ReviewAxisEnvelope{
			{
				Axis:         "bugs",
				Ran:          true,
				InputTokens:  &inTokens,
				OutputTokens: &outTokens,
				RawEnvelope:  `{"findings":[{"Severity":"HIGH","Confidence":0.9,"File":"main.go","Line":42,"Message":"unhandled error","Evidence":"err ignored","Remedy":"check err"}],"assurances":["input validation checked at every call site"]}`,
			},
			{
				Axis:        "quality",
				Ran:         true,
				RawEnvelope: `{"findings":[],"assurances":[]}`,
			},
			{
				Axis:   "docs",
				Ran:    false,
				Reason: "agentreviewer: axis docs: unrecoverable after 2 attempt(s): boom",
			},
		},
	}
	if err := store.RecordReviewRun(ctx, run); err != nil {
		t.Fatalf("RecordReviewRun: %v", err)
	}

	runs, err := store.ReviewRunsByIssue(ctx, "exec-review-5", "issue-review-5")
	if err != nil {
		t.Fatalf("ReviewRunsByIssue: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d review runs, want 1", len(runs))
	}
	got := runs[0].Envelopes
	if len(got) != 3 {
		t.Fatalf("got %d axis envelopes, want 3: %+v", len(got), got)
	}

	bugs := got[0]
	if bugs.Axis != "bugs" || !bugs.Ran || bugs.Reason != "" {
		t.Errorf("bugs envelope = %+v, want Axis bugs, Ran true, Reason empty", bugs)
	}
	if bugs.InputTokens == nil || *bugs.InputTokens != inTokens {
		t.Errorf("bugs.InputTokens = %v, want %d", bugs.InputTokens, inTokens)
	}
	if bugs.OutputTokens == nil || *bugs.OutputTokens != outTokens {
		t.Errorf("bugs.OutputTokens = %v, want %d", bugs.OutputTokens, outTokens)
	}
	if bugs.RawEnvelope != run.Envelopes[0].RawEnvelope {
		t.Errorf("bugs.RawEnvelope = %q, want %q", bugs.RawEnvelope, run.Envelopes[0].RawEnvelope)
	}
	var bugsDecoded struct {
		Findings   []map[string]any `json:"findings"`
		Assurances []string         `json:"assurances"`
	}
	if err := json.Unmarshal([]byte(bugs.RawEnvelope), &bugsDecoded); err != nil {
		t.Fatalf("unmarshal bugs.RawEnvelope: %v", err)
	}
	if len(bugsDecoded.Findings) != 1 || bugsDecoded.Findings[0]["Message"] != "unhandled error" {
		t.Errorf("bugsDecoded.Findings = %+v, want one finding with Message %q", bugsDecoded.Findings, "unhandled error")
	}
	wantAssurances := []string{"input validation checked at every call site"}
	if len(bugsDecoded.Assurances) != 1 || bugsDecoded.Assurances[0] != wantAssurances[0] {
		t.Errorf("bugsDecoded.Assurances = %v, want %v", bugsDecoded.Assurances, wantAssurances)
	}

	quality := got[1]
	if quality.Axis != "quality" || !quality.Ran || quality.RawEnvelope != `{"findings":[],"assurances":[]}` {
		t.Errorf("quality envelope = %+v, want Axis quality, Ran true, RawEnvelope {findings:[] assurances:[]}", quality)
	}
	if quality.InputTokens != nil || quality.OutputTokens != nil {
		t.Errorf("quality envelope tokens = %v/%v, want nil/nil", quality.InputTokens, quality.OutputTokens)
	}

	docs := got[2]
	if docs.Axis != "docs" || docs.Ran || docs.Reason != run.Envelopes[2].Reason {
		t.Errorf("docs envelope = %+v, want Axis docs, Ran false, Reason %q", docs, run.Envelopes[2].Reason)
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

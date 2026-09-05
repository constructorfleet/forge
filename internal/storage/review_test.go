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

	runs, err := store.ReviewRunsByIssueWithoutDiff(ctx, "exec-review", "issue-review")
	if err != nil {
		t.Fatalf("ReviewRunsByIssueWithoutDiff: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d review runs, want 1", len(runs))
	}
	got := runs[0]
	if got.Verdict != "APPROVED" || got.Summary != "looks good" {
		t.Errorf("got = %+v, want Verdict APPROVED Summary %q", got, "looks good")
	}
	if got.Diff != "" {
		t.Errorf("Diff = %q, want empty from ReviewRunsByIssueWithoutDiff", got.Diff)
	}
	if len(got.Findings) != 0 {
		t.Errorf("got %d findings, want 0", len(got.Findings))
	}
	if !got.StartedAt.Equal(started) || !got.FinishedAt.Equal(finished) {
		t.Errorf("StartedAt/FinishedAt = %v/%v, want %v/%v", got.StartedAt, got.FinishedAt, started, finished)
	}

	diff, err := store.LatestReviewDiff(ctx, "exec-review", "issue-review")
	if err != nil {
		t.Fatalf("LatestReviewDiff: %v", err)
	}
	if diff != "diff --git a b" {
		t.Errorf("LatestReviewDiff = %q, want %q", diff, "diff --git a b")
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

	runs, err := store.ReviewRunsByIssueWithoutDiff(ctx, "exec-review-2", "issue-review-2")
	if err != nil {
		t.Fatalf("ReviewRunsByIssueWithoutDiff: %v", err)
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
// RecordReviewRun/ReviewRunsByIssueWithoutDiff exactly as given, so a past
// Review is fully reconstructable.
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

	runs, err := store.ReviewRunsByIssueWithoutDiff(ctx, "exec-review-5", "issue-review-5")
	if err != nil {
		t.Fatalf("ReviewRunsByIssueWithoutDiff: %v", err)
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

func TestReviewRunsByIssueWithoutDiff_ReturnsEmptyForIssueWithNoReviewRuns(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForReviewRun(t, store, "exec-review-4", "issue-review-4")

	runs, err := store.ReviewRunsByIssueWithoutDiff(ctx, "exec-review-4", "issue-review-4")
	if err != nil {
		t.Fatalf("ReviewRunsByIssueWithoutDiff: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("got %d review runs, want 0", len(runs))
	}
}

// TestLatestReviewOutcome_ReadsTheLastRunWithoutTheDiff proves the observer
// seam returns the current verdict plus whether a diff exists, and never the
// diff body: a poll must not read a blob it discards.
func TestLatestReviewOutcome_ReadsTheLastRunWithoutTheDiff(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForReviewRun(t, store, "exec-review-6", "issue-review-6")

	first := storage.ReviewRun{
		ExecutionID: "exec-review-6",
		IssueID:     "issue-review-6",
		Verdict:     "CHANGES_REQUIRED",
		Summary:     "first pass",
		Diff:        "diff --git a/a.go b/a.go\n",
		StartedAt:   time.Now().UTC(),
		FinishedAt:  time.Now().UTC(),
	}
	if err := store.RecordReviewRun(ctx, first); err != nil {
		t.Fatalf("RecordReviewRun: %v", err)
	}
	second := first
	second.Verdict = "APPROVED"
	second.Summary = "second pass"
	second.Diff = ""
	if err := store.RecordReviewRun(ctx, second); err != nil {
		t.Fatalf("RecordReviewRun: %v", err)
	}

	got, err := store.LatestReviewOutcome(ctx, "exec-review-6", "issue-review-6")
	if err != nil {
		t.Fatalf("LatestReviewOutcome: %v", err)
	}
	if !got.Recorded {
		t.Fatal("Recorded = false, want true")
	}
	if got.Verdict != "APPROVED" {
		t.Errorf("Verdict = %q, want APPROVED", got.Verdict)
	}
	if got.HasDiff {
		t.Error("HasDiff = true, want false for a run that stored an empty diff")
	}
}

// TestLatestReviewOutcome_WithoutARunReportsNothing proves an unreviewed Issue
// reports no outcome instead of an error.
func TestLatestReviewOutcome_WithoutARunReportsNothing(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForReviewRun(t, store, "exec-review-7", "issue-review-7")

	got, err := store.LatestReviewOutcome(ctx, "exec-review-7", "issue-review-7")
	if err != nil {
		t.Fatalf("LatestReviewOutcome: %v", err)
	}
	if got.Recorded || got.Verdict != "" || got.HasDiff {
		t.Errorf("outcome = %+v, want a zero outcome", got)
	}
}

// TestReviewRunsByIssueWithoutDiff_OmitsDiffButKeepsEverythingElse proves the
// caller-selected variant (issue #540) drops only the diff blob: verdict,
// summary, findings, and axis envelopes all still round-trip.
func TestReviewRunsByIssueWithoutDiff_OmitsDiffButKeepsEverythingElse(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForReviewRun(t, store, "exec-review-8", "issue-review-8")

	run := storage.ReviewRun{
		ExecutionID: "exec-review-8",
		IssueID:     "issue-review-8",
		Verdict:     "CHANGES_REQUIRED",
		Summary:     "one blocking issue",
		Diff:        "diff --git a b",
		StartedAt:   time.Now(),
		FinishedAt:  time.Now(),
		Findings: []storage.ReviewFinding{
			{Severity: "ERROR", File: "main.go", Line: 42, Message: "unhandled error"},
		},
		Envelopes: []storage.ReviewAxisEnvelope{
			{Axis: "bugs", Ran: true, RawEnvelope: `{"findings":[],"assurances":[]}`},
		},
	}
	if err := store.RecordReviewRun(ctx, run); err != nil {
		t.Fatalf("RecordReviewRun: %v", err)
	}

	runs, err := store.ReviewRunsByIssueWithoutDiff(ctx, "exec-review-8", "issue-review-8")
	if err != nil {
		t.Fatalf("ReviewRunsByIssueWithoutDiff: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d review runs, want 1", len(runs))
	}
	got := runs[0]
	if got.Diff != "" {
		t.Errorf("Diff = %q, want empty", got.Diff)
	}
	if got.Verdict != "CHANGES_REQUIRED" || got.Summary != "one blocking issue" {
		t.Errorf("got = %+v, want Verdict CHANGES_REQUIRED Summary %q", got, "one blocking issue")
	}
	if len(got.Findings) != 1 || got.Findings[0].Message != "unhandled error" {
		t.Errorf("Findings = %+v, want one finding with Message %q", got.Findings, "unhandled error")
	}
	if len(got.Envelopes) != 1 || got.Envelopes[0].Axis != "bugs" {
		t.Errorf("Envelopes = %+v, want one envelope with Axis bugs", got.Envelopes)
	}
}

// TestReviewRunsByIssueWithoutDiff_GroupsFindingsAndEnvelopesPerRun proves that scanning
// a joined result set attributes each Finding and each ReviewAxisEnvelope to
// its own ReviewRun and no other, even when runs carry different counts of
// each — guarding the join-based scan against cross-run leakage.
func TestReviewRunsByIssueWithoutDiff_GroupsFindingsAndEnvelopesPerRun(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForReviewRun(t, store, "exec-review-9", "issue-review-9")

	first := storage.ReviewRun{
		ExecutionID: "exec-review-9",
		IssueID:     "issue-review-9",
		Verdict:     "CHANGES_REQUIRED",
		Summary:     "first pass",
		StartedAt:   time.Now(),
		FinishedAt:  time.Now(),
		Findings: []storage.ReviewFinding{
			{Severity: "ERROR", File: "a.go", Line: 1, Message: "first finding one"},
			{Severity: "WARNING", File: "b.go", Line: 2, Message: "first finding two"},
		},
		Envelopes: []storage.ReviewAxisEnvelope{
			{Axis: "bugs", Ran: true, RawEnvelope: "first bugs envelope"},
		},
	}
	if err := store.RecordReviewRun(ctx, first); err != nil {
		t.Fatalf("RecordReviewRun first: %v", err)
	}

	second := storage.ReviewRun{
		ExecutionID: "exec-review-9",
		IssueID:     "issue-review-9",
		Verdict:     "APPROVED",
		Summary:     "second pass",
		StartedAt:   time.Now(),
		FinishedAt:  time.Now(),
		Findings:    nil,
		Envelopes: []storage.ReviewAxisEnvelope{
			{Axis: "bugs", Ran: true, RawEnvelope: "second bugs envelope"},
			{Axis: "quality", Ran: true, RawEnvelope: "second quality envelope"},
			{Axis: "docs", Ran: false, Reason: "unrecoverable"},
		},
	}
	if err := store.RecordReviewRun(ctx, second); err != nil {
		t.Fatalf("RecordReviewRun second: %v", err)
	}

	runs, err := store.ReviewRunsByIssueWithoutDiff(ctx, "exec-review-9", "issue-review-9")
	if err != nil {
		t.Fatalf("ReviewRunsByIssueWithoutDiff: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d review runs, want 2", len(runs))
	}

	if len(runs[0].Findings) != 2 || len(runs[0].Envelopes) != 1 {
		t.Fatalf("runs[0] Findings=%d Envelopes=%d, want 2 and 1: %+v", len(runs[0].Findings), len(runs[0].Envelopes), runs[0])
	}
	if runs[0].Findings[0].Message != "first finding one" || runs[0].Findings[1].Message != "first finding two" {
		t.Errorf("runs[0].Findings = %+v, want ordered first finding one/two", runs[0].Findings)
	}
	if runs[0].Envelopes[0].RawEnvelope != "first bugs envelope" {
		t.Errorf("runs[0].Envelopes[0].RawEnvelope = %q, want %q", runs[0].Envelopes[0].RawEnvelope, "first bugs envelope")
	}

	if len(runs[1].Findings) != 0 || len(runs[1].Envelopes) != 3 {
		t.Fatalf("runs[1] Findings=%d Envelopes=%d, want 0 and 3: %+v", len(runs[1].Findings), len(runs[1].Envelopes), runs[1])
	}
	wantAxes := []string{"bugs", "quality", "docs"}
	for i, axis := range wantAxes {
		if runs[1].Envelopes[i].Axis != axis {
			t.Errorf("runs[1].Envelopes[%d].Axis = %q, want %q", i, runs[1].Envelopes[i].Axis, axis)
		}
	}
}

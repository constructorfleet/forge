package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/gate/gatetest"
	"github.com/Teagan42/forge/internal/review"
)

// stubDiff is a minimal engine.DiffProducer double: it records every call
// and returns a fixed diff string (or a programmed error), so tests never
// shell out to git.
type stubDiff struct {
	diff string
	err  error

	calls []struct{ workspacePath, base string }
}

func (s *stubDiff) Diff(_ context.Context, workspacePath, base string) (string, error) {
	s.calls = append(s.calls, struct{ workspacePath, base string }{workspacePath, base})
	if s.err != nil {
		return "", s.err
	}
	return s.diff, nil
}

// TestExecute_ReviewApproved_AdvancesToCommitting is ticket 20's first
// integration test: gates pass, Review returns APPROVED, and the Issue
// advances to COMMITTING.
func TestExecute_ReviewApproved_AdvancesToCommitting(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"30": {ID: "30"},
	})
	te.fake.ProgramResult("30", agent.AgentResult{Status: agent.StatusImplemented})

	reviewer := review.NewFakeReviewer()
	reviewer.ProgramResult("30", review.Result{Verdict: review.VerdictApproved, Summary: "ship it"})
	diff := &stubDiff{diff: "diff --git a/foo b/foo"}
	te.eng.Reviewer = reviewer
	te.eng.Diff = diff

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "30", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}

	state, err := te.store.LoadExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("LoadExecution: %v", err)
	}
	if len(state.Issues) != 1 || state.Issues[0].State != domain.StateCommitting {
		t.Fatalf("persisted issues = %+v, want one Issue in COMMITTING", state.Issues)
	}

	// The Reviewer received the diff (from the injected DiffProducer), the
	// Issue, and Repository Context — but nothing resembling implementation
	// conversation history (there is no such field on review.Request at
	// all, so this is really asserting the fields that ARE there are
	// correctly populated).
	invocations := reviewer.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("got %d reviewer invocations, want 1", len(invocations))
	}
	inv := invocations[0]
	if inv.Diff != "diff --git a/foo b/foo" {
		t.Errorf("Diff = %q, want %q", inv.Diff, "diff --git a/foo b/foo")
	}
	if inv.Issue.ID != "30" {
		t.Errorf("Issue.ID = %q, want %q", inv.Issue.ID, "30")
	}
	if inv.Repository.BaseRevision != te.base {
		t.Errorf("Repository.BaseRevision = %q, want %q", inv.Repository.BaseRevision, te.base)
	}

	// The DiffProducer was invoked against the Workspace path and the
	// Worker's base revision, not shelled out to inside Engine itself.
	if len(diff.calls) != 1 || diff.calls[0].base != te.base {
		t.Fatalf("diff calls = %+v, want one call with base %q", diff.calls, te.base)
	}

	// The Review run was persisted.
	runs, err := te.store.ReviewRunsByIssue(ctx, result.ExecutionID, "30")
	if err != nil {
		t.Fatalf("ReviewRunsByIssue: %v", err)
	}
	if len(runs) != 1 || runs[0].Verdict != "APPROVED" || runs[0].Summary != "ship it" {
		t.Fatalf("persisted review runs = %+v, want one APPROVED run", runs)
	}

	// The audit log carries the VALIDATING -> REVIEWING -> COMMITTING
	// transitions plus a "review.run" Event.
	events, err := te.store.EventsByExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	var sawReviewRun, sawCommitting bool
	for _, e := range events {
		if e.Type == "review.run" {
			sawReviewRun = true
		}
		if e.Type == "issue.transitioned" {
			var tr struct {
				To string `json:"to"`
			}
			if err := json.Unmarshal([]byte(e.Data), &tr); err == nil && tr.To == string(domain.StateCommitting) {
				sawCommitting = true
			}
		}
	}
	if !sawReviewRun {
		t.Error("no review.run event found")
	}
	if !sawCommitting {
		t.Error("no transition to COMMITTING found in events")
	}
}

// TestExecute_ReviewChangesRequired_PersistsFindingsAndExhaustsToFailed
// is ticket 20's second integration test, updated for ticket 21's repair
// loop: gates pass, Review returns CHANGES_REQUIRED, and — with the review
// retry budget pinned to 0 so this stays a single Review attempt — the
// Issue is routed straight to FAILED once that budget is exhausted, with
// the structured Findings persisted. (The full retry path, where a
// remaining budget instead re-invokes the Agent with these Findings as
// agent.Feedback per review.BuildFeedback, has its own dedicated tests in
// retry_test.go.)
func TestExecute_ReviewChangesRequired_PersistsFindingsAndExhaustsToFailed(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"31": {ID: "31"},
	})
	te.fake.ProgramResult("31", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Retry.Review = 0

	reviewer := review.NewFakeReviewer()
	reviewer.ProgramResult("31", review.Result{
		Verdict: review.VerdictChangesRequired,
		Summary: "one blocking issue",
		Findings: []review.Finding{
			{Severity: review.SeverityError, File: "main.go", Line: 42, Message: "unhandled error"},
		},
	})
	te.eng.Reviewer = reviewer
	te.eng.Diff = &stubDiff{diff: "diff --git a/main.go b/main.go"}

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "31", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("final state = %s, want FAILED", result.Issue.State)
	}

	runs, err := te.store.ReviewRunsByIssue(ctx, result.ExecutionID, "31")
	if err != nil {
		t.Fatalf("ReviewRunsByIssue: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d review runs, want 1", len(runs))
	}
	if len(runs[0].Findings) != 1 {
		t.Fatalf("got %d persisted findings, want 1", len(runs[0].Findings))
	}
	f := runs[0].Findings[0]
	if f.Severity != "ERROR" || f.File != "main.go" || f.Line != 42 || f.Message != "unhandled error" {
		t.Errorf("persisted finding = %+v, want ERROR main.go:42 %q", f, "unhandled error")
	}

	// The Agent was never re-invoked: the review budget was already
	// exhausted before a repair could be attempted.
	if got := len(te.fake.Invocations()); got != 1 {
		t.Errorf("got %d agent invocations, want 1 (no repair attempted)", got)
	}
}

func TestExecute_ReviewerUnset_ReviewingStaysRestingState(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"32": {ID: "32"},
	})
	te.fake.ProgramResult("32", agent.AgentResult{Status: agent.StatusImplemented})
	// te.eng.Reviewer intentionally left nil.

	result, err := te.eng.Execute(context.Background(), "32", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateReviewing {
		t.Fatalf("final state = %s, want REVIEWING (Reviewer unset, optional seam)", result.Issue.State)
	}
}

func TestExecute_ReviewerSetWithoutDiffProducer_FailsWithDescriptiveError(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"33": {ID: "33"},
	})
	te.fake.ProgramResult("33", agent.AgentResult{Status: agent.StatusImplemented})
	reviewer := review.NewFakeReviewer()
	reviewer.ProgramDefault(review.Result{Verdict: review.VerdictApproved})
	te.eng.Reviewer = reviewer
	// te.eng.Diff intentionally left nil.

	if _, err := te.eng.Execute(context.Background(), "33", te.base); err == nil {
		t.Fatal("Execute: want error when Reviewer is set but Diff is nil")
	}
}

func TestExecute_ReviewerError_FailsOutAndCleansUpWorkspace(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"34": {ID: "34"},
	})
	te.fake.ProgramResult("34", agent.AgentResult{Status: agent.StatusImplemented})
	reviewer := review.NewFakeReviewer()
	reviewer.ProgramError("34", errors.New("boom: reviewer backend crashed"))
	te.eng.Reviewer = reviewer
	te.eng.Diff = &stubDiff{diff: "diff"}

	if _, err := te.eng.Execute(context.Background(), "34", te.base); err == nil {
		t.Fatal("Execute: want error when the Reviewer errors")
	}
	if !te.ws.CleanupCalled() {
		t.Error("Cleanup was not called after a Reviewer error, want the orphaned Workspace removed")
	}
}

// TestExecute_QualityGateFails_ReviewerNeverInvoked guards against a
// regression that would run Review even though Quality Gates failed: the
// engine must only reach REVIEWING (and therefore only invoke Reviewer) via
// runQualityGates' passing path.
func TestExecute_QualityGateFails_ReviewerNeverInvoked(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"35": {ID: "35"},
	})
	te.fake.ProgramResult("35", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Quality.Gates = []config.QualityGate{
		{Name: "test", Command: "make test"},
	}
	// A zero gate budget keeps this a single-attempt check (the repair loop
	// itself, including a retry that eventually reaches Review, has its own
	// dedicated tests in retry_test.go).
	te.eng.Config.Retry.Gate = 0
	runner := gatetest.NewFakeCommandRunner()
	runner.ProgramResult("make test", 1, "failure output", "")
	te.eng.Gates = runner
	reviewer := review.NewFakeReviewer()
	te.eng.Reviewer = reviewer
	te.eng.Diff = &stubDiff{diff: "diff"}

	result, err := te.eng.Execute(context.Background(), "35", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("final state = %s, want FAILED", result.Issue.State)
	}
	if len(reviewer.Invocations()) != 0 {
		t.Errorf("got %d reviewer invocations, want 0 when Quality Gates fail", len(reviewer.Invocations()))
	}
}

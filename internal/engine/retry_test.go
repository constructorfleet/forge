package engine_test

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/gate"
	"github.com/Teagan42/forge/internal/review"
)

// flakyRunner is a gate.CommandRunner double that fails every call up to
// and including failUntil, then succeeds on every subsequent call — a
// stand-in for "the Agent's repair fixed the gate" that gatetest's
// FakeCommandRunner (one fixed outcome per exact command string) cannot
// express on its own.
type flakyRunner struct {
	mu        sync.Mutex
	calls     int
	failUntil int
}

func (f *flakyRunner) Run(_ context.Context, _, _ string, _, stderr io.Writer) (int, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()
	if n <= f.failUntil {
		_, _ = io.WriteString(stderr, "boom: gate failed")
		return 1, nil
	}
	return 0, nil
}

func (f *flakyRunner) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var _ gate.CommandRunner = (*flakyRunner)(nil)

// TestExecute_GateFailure_RetriesThenPassesReachesCommitting is ticket 21's
// first integration test: a Quality Gate fails once, the retry budget has
// room, the Agent is re-invoked with only that failure's bounded
// diagnostic, the full gate set reruns and passes, Review approves, and the
// Issue reaches COMMITTING. It also covers "every retry attempt persisted"
// and "Workspace preserved across retries" for the gate-repair path.
func TestExecute_GateFailure_RetriesThenPassesReachesCommitting(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"40": {ID: "40"},
	})
	te.fake.ProgramResult("40", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}
	te.eng.Config.Retry = domain.RetryLimits{Gate: 1, Review: 1, CI: 1}
	runner := &flakyRunner{failUntil: 1}
	te.eng.Gates = runner

	reviewer := review.NewFakeReviewer()
	reviewer.ProgramResult("40", review.Result{Verdict: review.VerdictApproved, Summary: "ship it"})
	te.eng.Reviewer = reviewer
	te.eng.Diff = &stubDiff{diff: "diff --git a/foo b/foo"}

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "40", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}

	// The gate ran twice: the failing first attempt and the passing repair
	// rerun of the *full* configured gate set.
	if got := runner.Calls(); got != 2 {
		t.Fatalf("got %d gate calls, want 2 (fail then pass)", got)
	}
	runs, err := te.store.GateRunsByIssue(ctx, result.ExecutionID, "40")
	if err != nil {
		t.Fatalf("GateRunsByIssue: %v", err)
	}
	if len(runs) != 2 || runs[0].Passed || !runs[1].Passed {
		t.Fatalf("persisted gate runs = %+v, want [failed, passed]", runs)
	}

	// The gate retry budget was decremented by exactly the one gate
	// failure; the review budget (independent) is untouched.
	issue, err := te.store.GetIssue(ctx, result.ExecutionID, "40")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.RetryBudget.GateFailures() != 1 {
		t.Errorf("GateFailures() = %d, want 1", issue.RetryBudget.GateFailures())
	}
	if issue.RetryBudget.ReviewFailures() != 0 {
		t.Errorf("ReviewFailures() = %d, want 0 (independent of gate budget)", issue.RetryBudget.ReviewFailures())
	}

	// The Agent was re-invoked exactly once for the repair, with Feedback
	// bounded to only the new gate diagnostic — not empty (like the first
	// attempt) and not a full-history replay.
	invocations := te.fake.Invocations()
	if len(invocations) != 2 {
		t.Fatalf("got %d agent invocations, want 2 (initial + 1 repair)", len(invocations))
	}
	if len(invocations[0].Feedback) != 0 {
		t.Errorf("first invocation Feedback = %+v, want empty", invocations[0].Feedback)
	}
	if len(invocations[1].Feedback) != 1 {
		t.Fatalf("repair invocation Feedback = %+v, want exactly 1 entry", invocations[1].Feedback)
	}
	if invocations[1].Feedback[0].Source != agent.FeedbackSourceGate {
		t.Errorf("repair Feedback[0].Source = %s, want GATE", invocations[1].Feedback[0].Source)
	}

	// Workspace preserved: both invocations used the same path, and it was
	// never cleaned up.
	if invocations[0].WorkspacePath == "" || invocations[0].WorkspacePath != invocations[1].WorkspacePath {
		t.Errorf("WorkspacePath differs across retries: %q vs %q", invocations[0].WorkspacePath, invocations[1].WorkspacePath)
	}
	if te.ws.CleanupCalled() {
		t.Error("Cleanup was called across a retry, want the Workspace preserved")
	}

	// Every attempt is reflected in the audit log: two "agent.result"
	// Events (initial + repair) and one "gate.failed" diagnostic.
	events, err := te.store.EventsByExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	var agentResults, gateFailed int
	for _, e := range events {
		switch e.Type {
		case "agent.result":
			agentResults++
		case "gate.failed":
			gateFailed++
		}
	}
	if agentResults != 2 {
		t.Errorf("got %d agent.result events, want 2", agentResults)
	}
	if gateFailed != 1 {
		t.Errorf("got %d gate.failed events, want 1", gateFailed)
	}
}

// TestExecute_ReviewChangesRequired_RetriesThenApprovedReachesCommitting is
// ticket 21's second integration test: Review returns CHANGES_REQUIRED,
// the retry budget has room, the Agent is re-invoked with the structured
// findings as bounded Feedback, the full (empty, here) gate set reruns,
// Review is invoked again and approves, and the Issue reaches COMMITTING.
func TestExecute_ReviewChangesRequired_RetriesThenApprovedReachesCommitting(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"41": {ID: "41"},
	})
	te.fake.ProgramResult("41", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Retry = domain.RetryLimits{Gate: 1, Review: 1, CI: 1}

	reviewer := review.NewFakeReviewer()
	reviewer.ProgramResult("41", review.Result{
		Verdict: review.VerdictChangesRequired,
		Summary: "one blocking issue",
		Findings: []review.Finding{
			{Severity: review.SeverityError, File: "main.go", Line: 42, Message: "unhandled error"},
		},
	})
	reviewer.ProgramResult("41", review.Result{Verdict: review.VerdictApproved, Summary: "looks good now"})
	te.eng.Reviewer = reviewer
	te.eng.Diff = &stubDiff{diff: "diff --git a/main.go b/main.go"}

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "41", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}

	invocations := reviewer.Invocations()
	if len(invocations) != 2 {
		t.Fatalf("got %d reviewer invocations, want 2", len(invocations))
	}

	runs, err := te.store.ReviewRunsByIssue(ctx, result.ExecutionID, "41")
	if err != nil {
		t.Fatalf("ReviewRunsByIssue: %v", err)
	}
	if len(runs) != 2 || runs[0].Verdict != "CHANGES_REQUIRED" || runs[1].Verdict != "APPROVED" {
		t.Fatalf("persisted review runs = %+v, want [CHANGES_REQUIRED, APPROVED]", runs)
	}
	if len(runs[0].Findings) != 1 {
		t.Fatalf("got %d persisted findings on first run, want 1", len(runs[0].Findings))
	}

	issue, err := te.store.GetIssue(ctx, result.ExecutionID, "41")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.RetryBudget.ReviewFailures() != 1 {
		t.Errorf("ReviewFailures() = %d, want 1", issue.RetryBudget.ReviewFailures())
	}
	if issue.RetryBudget.GateFailures() != 0 {
		t.Errorf("GateFailures() = %d, want 0 (independent of review budget)", issue.RetryBudget.GateFailures())
	}

	// The Agent's repair invocation received exactly the one Finding as
	// bounded Feedback, not empty and not a full-history replay.
	agentInvocations := te.fake.Invocations()
	if len(agentInvocations) != 2 {
		t.Fatalf("got %d agent invocations, want 2 (initial + 1 repair)", len(agentInvocations))
	}
	if len(agentInvocations[0].Feedback) != 0 {
		t.Errorf("first invocation Feedback = %+v, want empty", agentInvocations[0].Feedback)
	}
	if len(agentInvocations[1].Feedback) != 1 || agentInvocations[1].Feedback[0].Source != agent.FeedbackSourceReview {
		t.Fatalf("repair invocation Feedback = %+v, want exactly 1 REVIEW entry", agentInvocations[1].Feedback)
	}

	// The full gate set was rerun before the second Review, even though no
	// gates are configured here: the Issue passed back through VALIDATING,
	// which the transition Event log makes visible.
	events, err := te.store.EventsByExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	var validatingCount int
	for _, e := range events {
		if e.Type != "issue.transitioned" {
			continue
		}
		var tr struct {
			To string `json:"to"`
		}
		if err := json.Unmarshal([]byte(e.Data), &tr); err == nil && tr.To == string(domain.StateValidating) {
			validatingCount++
		}
	}
	if validatingCount != 2 {
		t.Errorf("saw %d transitions to VALIDATING, want 2 (initial pass + repair rerun)", validatingCount)
	}
}

// TestExecute_GateBudgetExhaustion_RoutesToFailed is ticket 21's budget
// exhaustion integration test for the gate side: every repair still fails
// the same gate, so once the gate retry budget (independent from review's)
// is exhausted the Issue transitions to FAILED with its diagnostics
// preserved.
func TestExecute_GateBudgetExhaustion_RoutesToFailed(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"42": {ID: "42"},
	})
	te.fake.ProgramResult("42", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}
	te.eng.Config.Retry = domain.RetryLimits{Gate: 2, Review: 2, CI: 2}
	runner := &flakyRunner{failUntil: 1000} // never passes
	te.eng.Gates = runner

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "42", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("final state = %s, want FAILED", result.Issue.State)
	}

	// 1 initial attempt + 2 retries = 3 gate runs, and 3 Agent invocations
	// (initial + 2 repairs) — the budget stops the loop, not the Agent.
	if got := runner.Calls(); got != 3 {
		t.Errorf("got %d gate calls, want 3 (1 initial + 2 retries)", got)
	}
	if got := len(te.fake.Invocations()); got != 3 {
		t.Errorf("got %d agent invocations, want 3 (1 initial + 2 repairs)", got)
	}

	issue, err := te.store.GetIssue(ctx, result.ExecutionID, "42")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if !issue.RetryBudget.GateExhausted() {
		t.Error("GateExhausted() = false, want true")
	}
	if issue.RetryBudget.GateFailures() != 2 {
		t.Errorf("GateFailures() = %d, want 2", issue.RetryBudget.GateFailures())
	}
	if issue.RetryBudget.ReviewFailures() != 0 {
		t.Errorf("ReviewFailures() = %d, want 0 (never reached Review)", issue.RetryBudget.ReviewFailures())
	}

	runs, err := te.store.GateRunsByIssue(ctx, result.ExecutionID, "42")
	if err != nil {
		t.Fatalf("GateRunsByIssue: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d persisted gate runs, want 3 (one per attempt)", len(runs))
	}
}

// TestExecute_ReviewBudgetExhaustion_RoutesToFailed is ticket 21's budget
// exhaustion integration test for the review side: Review keeps returning
// CHANGES_REQUIRED, so once the review retry budget (independent from
// gate's) is exhausted the Issue transitions to FAILED.
func TestExecute_ReviewBudgetExhaustion_RoutesToFailed(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"43": {ID: "43"},
	})
	te.fake.ProgramResult("43", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Retry = domain.RetryLimits{Gate: 2, Review: 1, CI: 2}

	reviewer := review.NewFakeReviewer()
	reviewer.ProgramDefault(review.Result{
		Verdict: review.VerdictChangesRequired,
		Findings: []review.Finding{
			{Severity: review.SeverityError, File: "main.go", Line: 1, Message: "still broken"},
		},
	})
	te.eng.Reviewer = reviewer
	te.eng.Diff = &stubDiff{diff: "diff"}

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "43", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("final state = %s, want FAILED", result.Issue.State)
	}

	if got := len(reviewer.Invocations()); got != 2 {
		t.Errorf("got %d reviewer invocations, want 2 (1 initial + 1 retry)", got)
	}

	issue, err := te.store.GetIssue(ctx, result.ExecutionID, "43")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if !issue.RetryBudget.ReviewExhausted() {
		t.Error("ReviewExhausted() = false, want true")
	}
	if issue.RetryBudget.ReviewFailures() != 1 {
		t.Errorf("ReviewFailures() = %d, want 1", issue.RetryBudget.ReviewFailures())
	}
	if issue.RetryBudget.GateFailures() != 0 {
		t.Errorf("GateFailures() = %d, want 0 (independent of review budget)", issue.RetryBudget.GateFailures())
	}
}

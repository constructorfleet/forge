package engine_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/gate"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/review"
	"github.com/Teagan42/forge/internal/storage"
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
	te.gates.Set(runner)

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

	runs, err := te.store.ReviewRunsByIssueWithoutDiff(ctx, result.ExecutionID, "41")
	if err != nil {
		t.Fatalf("ReviewRunsByIssueWithoutDiff: %v", err)
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

// TestExecute_ReviewChangesRequired_EmitsFindingsRoutedEvent is issue
// #222's regression test: the only audit-log evidence that a
// CHANGES_REQUIRED verdict is being addressed, rather than merely
// recorded, was the generic "issue.transitioned" (REVIEWING->IMPLEMENTING)
// and "agent.result" Events, with nothing naming the Findings that drove
// that particular repair attempt or linking them to it. This asserts a
// lean "review.findings_routed" Event (mirroring "gate.failed") is
// appended, with a count and the blocking summary, before the repair
// Agent invocation runs.
func TestExecute_ReviewChangesRequired_EmitsFindingsRoutedEvent(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"43": {ID: "43"},
	})
	te.fake.ProgramResult("43", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Retry = domain.RetryLimits{Gate: 1, Review: 1, CI: 1}

	reviewer := review.NewFakeReviewer()
	reviewer.ProgramResult("43", review.Result{
		Verdict: review.VerdictChangesRequired,
		Summary: "two blocking issues",
		Findings: []review.Finding{
			{Severity: review.SeverityError, File: "main.go", Line: 42, Message: "unhandled error"},
			{Severity: review.SeverityWarning, File: "util.go", Line: 7, Message: "unused import"},
		},
	})
	reviewer.ProgramResult("43", review.Result{Verdict: review.VerdictApproved, Summary: "looks good now"})
	te.eng.Reviewer = reviewer
	te.eng.Diff = &stubDiff{diff: "diff --git a/main.go b/main.go"}

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "43", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}

	events, err := te.store.EventsByExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	var routed *storage.Event
	for i := range events {
		if events[i].Type == "review.findings_routed" {
			routed = &events[i]
		}
	}
	if routed == nil {
		t.Fatalf("no review.findings_routed event found among %+v", events)
	}
	if !strings.Contains(routed.Data, `"count":"2"`) {
		t.Errorf("review.findings_routed event data = %s, want it to contain a count of 2", routed.Data)
	}
	for _, want := range []string{"unhandled error", "unused import"} {
		if !strings.Contains(routed.Data, want) {
			t.Errorf("review.findings_routed event data = %s, want it to contain finding message %q", routed.Data, want)
		}
	}

	// The Event is appended before the repair Agent invocation runs, not
	// after: it should precede the second "agent.result" Event in the log.
	var routedIdx, secondAgentResultIdx = -1, -1
	agentResults := 0
	for i, e := range events {
		if e.Type == "review.findings_routed" {
			routedIdx = i
		}
		if e.Type == "agent.result" {
			agentResults++
			if agentResults == 2 {
				secondAgentResultIdx = i
			}
		}
	}
	if routedIdx == -1 || secondAgentResultIdx == -1 || routedIdx >= secondAgentResultIdx {
		t.Errorf("review.findings_routed (idx %d) should precede the repair's agent.result (idx %d)", routedIdx, secondAgentResultIdx)
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
	te.gates.Set(runner)

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

// TestExecute_GateRepair_AgentReturnsNeedsInfo_RoutesToNeedsInfo drives a
// gate-failure repair whose re-invoked Agent reports StatusNeedsInfo rather
// than StatusImplemented, asserting invokeAgent's non-StatusImplemented
// arms are reachable from a repair iteration too, not just Execute's first
// attempt.
func TestExecute_GateRepair_AgentReturnsNeedsInfo_RoutesToNeedsInfo(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"44": {ID: "44"},
	})
	te.fake.ProgramResult("44", agent.AgentResult{Status: agent.StatusImplemented})
	te.fake.ProgramResult("44", agent.AgentResult{
		Status:    agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{Question: "which config flag?"},
	})
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}
	te.eng.Config.Retry = domain.RetryLimits{Gate: 1, Review: 1, CI: 1}
	runner := &flakyRunner{failUntil: 1000} // never passes; repair's Agent call is what changes
	te.gates.Set(runner)

	result, err := te.eng.Execute(context.Background(), "44", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateNeedsInfo {
		t.Fatalf("final state = %s, want NEEDS_INFO", result.Issue.State)
	}
	if got := len(te.fake.Invocations()); got != 2 {
		t.Errorf("got %d agent invocations, want 2 (initial + 1 repair)", got)
	}
}

// TestExecute_GateRepair_AgentReturnsFailed_RoutesToFailed is the FAILED
// counterpart: the repair's re-invoked Agent itself reports StatusFailed.
func TestExecute_GateRepair_AgentReturnsFailed_RoutesToFailed(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"45": {ID: "45"},
	})
	te.fake.ProgramResult("45", agent.AgentResult{Status: agent.StatusImplemented})
	te.fake.ProgramResult("45", agent.AgentResult{Status: agent.StatusFailed, Summary: "gave up"})
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}
	te.eng.Config.Retry = domain.RetryLimits{Gate: 1, Review: 1, CI: 1}
	runner := &flakyRunner{failUntil: 1000}
	te.gates.Set(runner)

	result, err := te.eng.Execute(context.Background(), "45", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("final state = %s, want FAILED", result.Issue.State)
	}
	if got := len(te.fake.Invocations()); got != 2 {
		t.Errorf("got %d agent invocations, want 2 (initial + 1 repair)", got)
	}

	// The gate ran only once: the repair's Agent call failed outright, so
	// the loop never got back to a second gate rerun.
	if got := runner.Calls(); got != 1 {
		t.Errorf("got %d gate calls, want 1 (repair Agent failed before a rerun)", got)
	}
}

// TestExecute_ReviewBudgetExhaustion_RoutesToFailed is ticket 21's budget
// exhaustion integration test for the review side: Review keeps returning
// CHANGES_REQUIRED, so once the review retry budget (independent from
// gate's) is exhausted the Issue transitions to FAILED.
// TestExecute_ReviewBudgetExhaustion_RoutesToNeedsInfo asserts issue #161's
// escalation: a standing CHANGES_REQUIRED verdict, once RetryBudget.Review
// is exhausted, routes to NEEDS_INFO rather than the FAILED terminal this
// test asserted before #161 (nothing here is the implementation's fault, so
// FAILED is the wrong terminal, and approving on a standing rejection would
// be the false-approval Forge must never produce).
func TestExecute_ReviewBudgetExhaustion_RoutesToNeedsInfo(t *testing.T) {
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
	if result.Issue.State != domain.StateNeedsInfo {
		t.Fatalf("final state = %s, want NEEDS_INFO", result.Issue.State)
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

// TestExecute_ReviewNonConvergentFinding_EscalatesBeforeBudgetExhausted is
// issue #375's core case: a review axis repeats the exact same Finding
// against unchanged code on the very next retry. Even though the review
// retry budget (Review: 3) has plenty of room left, the repeated Finding
// itself must trigger an early NEEDS_INFO escalation rather than burning
// the remaining budget on an Agent that has nothing left to change.
func TestExecute_ReviewNonConvergentFinding_EscalatesBeforeBudgetExhausted(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"375": {ID: "375"},
	})
	te.fake.ProgramResult("375", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Retry = domain.RetryLimits{Gate: 2, Review: 3, CI: 2}

	reviewer := review.NewFakeReviewer()
	reviewer.ProgramDefault(review.Result{
		Verdict: review.VerdictChangesRequired,
		Findings: []review.Finding{
			{Severity: review.SeverityError, Axis: "bugs", File: "main.go", Line: 1, Message: "still broken"},
		},
	})
	te.eng.Reviewer = reviewer
	te.eng.Diff = &stubDiff{diff: "diff"}

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "375", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateNeedsInfo {
		t.Fatalf("final state = %s, want NEEDS_INFO", result.Issue.State)
	}

	// Escalation must fire on the second identical review, not after the
	// third retry the Review:3 budget would otherwise allow.
	if got := len(reviewer.Invocations()); got != 2 {
		t.Errorf("got %d reviewer invocations, want 2 (initial + 1 repeat, escalated before a 3rd)", got)
	}

	issue, err := te.store.GetIssue(ctx, result.ExecutionID, "375")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got := issue.RetryBudget.ReviewFailures(); got != 1 {
		t.Errorf("ReviewFailures() = %d, want 1 (budget not burned on the non-convergent repeat)", got)
	}

	overrides, err := te.store.ReviewOverridesByIssue(ctx, "375")
	if err != nil {
		t.Fatalf("ReviewOverridesByIssue: %v", err)
	}
	if len(overrides) != 1 {
		t.Fatalf("got %d review overrides, want 1", len(overrides))
	}
	if overrides[0].Message != "still broken" || overrides[0].Axis != "bugs" {
		t.Errorf("override = %+v, want Message %q Axis %q", overrides[0], "still broken", "bugs")
	}
}

// TestExecute_ReviewNonConvergentFinding_EscalationContextIncludesAllFindings
// asserts the escalation's NEEDS_INFO context reports every standing
// Finding from the triggering review, not only the non-convergent one: a
// review round can return one repeated Finding alongside other, different
// Findings, and a human resolving the escalation must see all of them.
func TestExecute_ReviewNonConvergentFinding_EscalationContextIncludesAllFindings(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"377": {ID: "377"},
	})
	te.fake.ProgramResult("377", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Retry = domain.RetryLimits{Gate: 2, Review: 3, CI: 2}

	reviewer := review.NewFakeReviewer()
	reviewer.ProgramResult("377", review.Result{
		Verdict: review.VerdictChangesRequired,
		Findings: []review.Finding{
			{Severity: review.SeverityError, Axis: "bugs", File: "main.go", Line: 1, Message: "still broken"},
			{Severity: review.SeverityWarning, Axis: "docs", File: "readme.md", Line: 2, Message: "missing section"},
		},
	})
	reviewer.ProgramResult("377", review.Result{
		Verdict: review.VerdictChangesRequired,
		Findings: []review.Finding{
			{Severity: review.SeverityError, Axis: "bugs", File: "main.go", Line: 1, Message: "still broken"},
			{Severity: review.SeverityWarning, Axis: "docs", File: "readme.md", Line: 2, Message: "a different complaint"},
		},
	})
	te.eng.Reviewer = reviewer
	te.eng.Diff = &stubDiff{diff: "diff"}

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "377", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateNeedsInfo {
		t.Fatalf("final state = %s, want NEEDS_INFO", result.Issue.State)
	}

	checkpoint, err := te.store.GetNeedsInfoCheckpoint(ctx, result.ExecutionID, "377")
	if err != nil {
		t.Fatalf("GetNeedsInfoCheckpoint: %v", err)
	}
	if !strings.Contains(checkpoint.Context, "still broken") {
		t.Errorf("checkpoint context = %q, want it to mention the non-convergent finding %q", checkpoint.Context, "still broken")
	}
	if !strings.Contains(checkpoint.Context, "a different complaint") {
		t.Errorf("checkpoint context = %q, want it to also mention the other standing finding %q", checkpoint.Context, "a different complaint")
	}
}

// TestExecute_ReviewOverride_SuppressesRepeatedFindingInNewExecution
// asserts the "survives re-runs" half of issue #375: once a non-convergent
// Finding has been escalated once (persisting a ReviewOverride keyed by
// IssueID), a later Execute for the same Issue — a brand new Execution,
// e.g. after a human resumes it — must not spend any review retries on the
// same Finding again; it is suppressed immediately and the Issue reaches
// COMMITTING.
func TestExecute_ReviewOverride_SuppressesRepeatedFindingInNewExecution(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"376": {ID: "376"},
	})
	te.fake.ProgramResult("376", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Retry = domain.RetryLimits{Gate: 2, Review: 3, CI: 2}

	reviewer := review.NewFakeReviewer()
	reviewer.ProgramDefault(review.Result{
		Verdict: review.VerdictChangesRequired,
		Findings: []review.Finding{
			{Severity: review.SeverityError, Axis: "bugs", File: "main.go", Line: 1, Message: "still broken"},
		},
	})
	te.eng.Reviewer = reviewer
	te.eng.Diff = &stubDiff{diff: "diff"}

	ctx := context.Background()
	first, err := te.eng.Execute(ctx, "376", te.base)
	if err != nil {
		t.Fatalf("Execute (1st): %v", err)
	}
	if first.Issue.State != domain.StateNeedsInfo {
		t.Fatalf("1st run final state = %s, want NEEDS_INFO", first.Issue.State)
	}

	// A brand new Execution for the same Issue. te.eng.Reviewer keeps
	// returning the identical CHANGES_REQUIRED Finding; the persisted
	// override must suppress it immediately.
	second, err := te.eng.Execute(ctx, "376", te.base)
	if err != nil {
		t.Fatalf("Execute (2nd): %v", err)
	}
	if second.Issue.State != domain.StateCommitting {
		t.Fatalf("2nd run final state = %s, want COMMITTING (override should suppress the repeated finding)", second.Issue.State)
	}
	if second.ExecutionID == first.ExecutionID {
		t.Fatalf("2nd run reused the 1st run's ExecutionID; test needs a genuinely new Execution")
	}

	issue, err := te.store.GetIssue(ctx, second.ExecutionID, "376")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got := issue.RetryBudget.ReviewFailures(); got != 0 {
		t.Errorf("ReviewFailures() = %d, want 0 (override suppressed the finding on the first review)", got)
	}
}

func TestRepairCIFailure_RetriesInSameWorkspaceReachesCIPending(t *testing.T) {
	te := approvedTestEngine(t, "46", domain.Issue{ID: "46", Title: "repair CI"})
	pub := &fakePublisher{commitSHA: "sha-1"}
	prTracker := newFakePRTracker()
	te.eng.Publisher = pub
	te.eng.PRTracker = prTracker
	te.eng.BaseBranch = "main"
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}
	te.eng.Config.Retry = domain.RetryLimits{Gate: 1, Review: 1, CI: 1}
	runner := &flakyRunner{failUntil: 0}
	te.gates.Set(runner)

	ctx := context.Background()
	initial, err := te.eng.Execute(ctx, "46", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if initial.Issue.State != domain.StateCIPending {
		t.Fatalf("initial state = %s, want CI_PENDING", initial.Issue.State)
	}

	if err := te.store.RecordCIRun(ctx, storage.CIRun{
		ExecutionID: initial.ExecutionID,
		IssueID:     "46",
		Status:      storage.CIRunStatusFailed,
		CheckName:   "build",
		Details:     "stacktrace line 1\nstacktrace line 2",
		CheckedAt:   te.eng.Now(),
	}); err != nil {
		t.Fatalf("RecordCIRun: %v", err)
	}
	if _, err := te.store.TransitionIssue(ctx, initial.ExecutionID, "46", domain.StateCIFailed); err != nil {
		t.Fatalf("TransitionIssue(CI_FAILED): %v", err)
	}

	te.fake.ProgramResult("46", agent.AgentResult{Status: agent.StatusImplemented, Summary: "ci repaired"})

	repaired, err := te.eng.RepairCIFailure(ctx, initial.ExecutionID, "46")
	if err != nil {
		t.Fatalf("RepairCIFailure: %v", err)
	}
	if repaired.State != domain.StateCIPending {
		t.Fatalf("repaired state = %s, want CI_PENDING", repaired.State)
	}

	invocations := te.fake.Invocations()
	if len(invocations) != 2 {
		t.Fatalf("got %d agent invocations, want 2 (initial + CI repair)", len(invocations))
	}
	if invocations[1].WorkspacePath != invocations[0].WorkspacePath {
		t.Fatalf("repair WorkspacePath = %q, want same as initial %q", invocations[1].WorkspacePath, invocations[0].WorkspacePath)
	}
	if len(invocations[1].Feedback) != 1 {
		t.Fatalf("repair Feedback = %+v, want exactly 1 entry", invocations[1].Feedback)
	}
	if invocations[1].Feedback[0].Source != agent.FeedbackSourceCI {
		t.Fatalf("repair Feedback[0].Source = %s, want CI", invocations[1].Feedback[0].Source)
	}
	if got := invocations[1].Feedback[0].Message; got != "CI check failed:\nCheck: build\nDetails:\nstacktrace line 1\nstacktrace line 2" {
		t.Fatalf("repair Feedback[0].Message = %q", got)
	}

	if got := runner.Calls(); got != 2 {
		t.Fatalf("gate calls = %d, want 2 (initial + repair rerun)", got)
	}
	if got := pub.pushCallCount(); got != 2 {
		t.Fatalf("push calls = %d, want 2 (initial publish + repair publish)", got)
	}
	if got := prTracker.callCount(); got != 2 {
		t.Fatalf("CreatePullRequest calls = %d, want 2 (repair should recover existing PR)", got)
	}

	issue, err := te.store.GetIssue(ctx, initial.ExecutionID, "46")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.RetryBudget.CIFailures() != 1 {
		t.Fatalf("CIFailures() = %d, want 1", issue.RetryBudget.CIFailures())
	}
	if issue.RetryBudget.GateFailures() != 0 || issue.RetryBudget.ReviewFailures() != 0 {
		t.Fatalf("unexpected retry budget after CI repair: %+v", issue.RetryBudget)
	}

	events, err := te.store.EventsByIssue(ctx, initial.ExecutionID, "46")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	var sawCIFailed, sawCIPending bool
	for _, e := range events {
		if e.Type != "issue.transitioned" {
			continue
		}
		var tr struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.Unmarshal([]byte(e.Data), &tr); err != nil {
			t.Fatalf("unmarshal transition: %v", err)
		}
		if tr.From == string(domain.StateCIPending) && tr.To == string(domain.StateCIFailed) {
			sawCIFailed = true
		}
		if tr.From == string(domain.StatePRCreating) && tr.To == string(domain.StateCIPending) {
			sawCIPending = true
		}
	}
	if !sawCIFailed {
		t.Fatal("did not record CI_PENDING -> CI_FAILED transition")
	}
	if !sawCIPending {
		t.Fatal("did not record repaired PR_CREATING -> CI_PENDING transition")
	}
}

func TestRepairCIFailure_CIBudgetExhaustion_RoutesToFailed(t *testing.T) {
	te := approvedTestEngine(t, "47", domain.Issue{ID: "47", Title: "ci budget exhausted"})
	pub := &fakePublisher{commitSHA: "sha-1"}
	te.eng.Publisher = pub
	te.eng.PRTracker = newFakePRTracker()
	te.eng.BaseBranch = "main"
	te.eng.Config.Retry = domain.RetryLimits{Gate: 1, Review: 1, CI: 0}

	ctx := context.Background()
	initial, err := te.eng.Execute(ctx, "47", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := te.store.RecordCIRun(ctx, storage.CIRun{
		ExecutionID: initial.ExecutionID,
		IssueID:     "47",
		Status:      storage.CIRunStatusFailed,
		CheckName:   "test",
		Details:     "boom",
		CheckedAt:   te.eng.Now(),
	}); err != nil {
		t.Fatalf("RecordCIRun: %v", err)
	}
	if _, err := te.store.TransitionIssue(ctx, initial.ExecutionID, "47", domain.StateCIFailed); err != nil {
		t.Fatalf("TransitionIssue(CI_FAILED): %v", err)
	}

	repaired, err := te.eng.RepairCIFailure(ctx, initial.ExecutionID, "47")
	if err != nil {
		t.Fatalf("RepairCIFailure: %v", err)
	}
	if repaired.State != domain.StateFailed {
		t.Fatalf("repaired state = %s, want FAILED", repaired.State)
	}
	if got := len(te.fake.Invocations()); got != 1 {
		t.Fatalf("agent invocations = %d, want 1 (no CI repair attempt after exhaustion)", got)
	}

	issue, err := te.store.GetIssue(ctx, initial.ExecutionID, "47")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if !issue.RetryBudget.CIExhausted() {
		t.Fatal("CIExhausted() = false, want true")
	}
	if issue.RetryBudget.CIFailures() != 0 {
		t.Fatalf("CIFailures() = %d, want 0 (ceiling already exhausted before recording)", issue.RetryBudget.CIFailures())
	}
}

func TestRepairCIFailure_UsesCapturedWorkerBaseInsteadOfExecutionBase(t *testing.T) {
	te := approvedTestEngine(t, "48", domain.Issue{ID: "48", Title: "repair uses worker base"})
	pub := &fakePublisher{commitSHA: "sha-1"}
	te.eng.Publisher = pub
	te.eng.PRTracker = newFakePRTracker()
	te.eng.BaseBranch = "main"
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}
	te.eng.Config.Retry = domain.RetryLimits{Gate: 1, Review: 1, CI: 1}
	te.gates.Set(&flakyRunner{failUntil: 0})

	ctx := context.Background()
	gittest.RunGit(t, te.eng.RepoRoot, "commit", "--allow-empty", "-q", "-m", "worker base")
	workerBase := strings.TrimSpace(gittest.RunGit(t, te.eng.RepoRoot, "rev-parse", "HEAD"))
	sharedExec := domain.Execution{
		ID:           "exec-shared-48",
		BaseRevision: te.base,
		StartedAt:    te.eng.Now(),
	}
	if err := te.store.CreateExecution(ctx, sharedExec); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	initial, err := te.eng.ExecuteInExecution(ctx, sharedExec, "48", workerBase)
	if err != nil {
		t.Fatalf("ExecuteInExecution: %v", err)
	}
	if initial.Issue.State != domain.StateCIPending {
		t.Fatalf("initial state = %s, want CI_PENDING", initial.Issue.State)
	}

	if err := te.store.RecordCIRun(ctx, storage.CIRun{
		ExecutionID: initial.ExecutionID,
		IssueID:     "48",
		Status:      storage.CIRunStatusFailed,
		CheckName:   "build",
		Details:     "boom",
		CheckedAt:   te.eng.Now(),
	}); err != nil {
		t.Fatalf("RecordCIRun: %v", err)
	}
	if _, err := te.store.TransitionIssue(ctx, initial.ExecutionID, "48", domain.StateCIFailed); err != nil {
		t.Fatalf("TransitionIssue(CI_FAILED): %v", err)
	}

	te.fake.ProgramResult("48", agent.AgentResult{Status: agent.StatusImplemented, Summary: "ci repaired"})

	repaired, err := te.eng.RepairCIFailure(ctx, initial.ExecutionID, "48")
	if err != nil {
		t.Fatalf("RepairCIFailure: %v", err)
	}
	if repaired.State != domain.StateCIPending {
		t.Fatalf("repaired state = %s, want CI_PENDING", repaired.State)
	}

	invocations := te.fake.Invocations()
	if len(invocations) != 2 {
		t.Fatalf("got %d agent invocations, want 2", len(invocations))
	}
	if invocations[1].Repository.BaseRevision != workerBase {
		t.Fatalf("repair BaseRevision = %q, want captured worker base", invocations[1].Repository.BaseRevision)
	}
}

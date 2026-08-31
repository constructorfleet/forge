package agentreviewer_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/review"
	"github.com/Teagan42/forge/internal/review/agentreviewer"
)

const cleanEnvelope = `{"axis":"bugs","findings":[]}`

const highConfidenceEnvelope = `{
  "axis": "bugs",
  "findings": [
    {
      "severity": "HIGH",
      "confidence": 0.9,
      "file": "internal/foo/foo.go",
      "line": 42,
      "message": "nil pointer dereference on the error path",
      "evidence": "foo() returns (nil, err) but the caller dereferences the result before checking err",
      "remedy": "check err before dereferencing the result"
    }
  ]
}`

const lowConfidenceHighSeverityEnvelope = `{
  "axis": "bugs",
  "findings": [
    {
      "severity": "HIGH",
      "confidence": 0.4,
      "file": "internal/foo/foo.go",
      "line": 10,
      "message": "possible race, unconfirmed",
      "evidence": "two goroutines may touch this field but I could not fully trace the call sites",
      "remedy": "add a mutex if confirmed"
    }
  ]
}`

const highConfidenceMedSeverityEnvelope = `{
  "axis": "bugs",
  "findings": [
    {
      "severity": "MED",
      "confidence": 0.9,
      "file": "internal/foo/foo.go",
      "line": 43,
      "message": "review feedback is dropped before repair",
      "evidence": "the finding is emitted by the review axis but the worker is never re-invoked",
      "remedy": "route MED-and-higher findings as changes required"
    }
  ]
}`

const qualityEnvelope = `{
  "axis": "quality",
  "findings": [
    {
      "severity": "HIGH",
      "confidence": 0.85,
      "file": "internal/foo/foo.go",
      "line": 42,
      "message": "this function has grown three responsibilities",
      "evidence": "foo() now parses, validates, and persists in one function body",
      "remedy": "split into parse/validate/persist helpers"
    }
  ]
}`

// axisMarker is a substring unique to each embedded rubric's title, used by
// axisRoutingAgent to identify which axis a given AgentRequest belongs to
// without relying on invocation/queue order (fan-out order across the three
// concurrent axis calls is nondeterministic).
const (
	bugsAxisMarker    = "Bugs & Security Review Axis"
	qualityAxisMarker = "Code-Quality & Maintainability Review Axis"
	docsAxisMarker    = "Documentation Review Axis"
)

// axisRoutingAgent is a deterministic agent.Agent test double for
// Reviewer's concurrent 3-axis fan-out. Because the three axes' Execute
// calls race, a FakeAgent's per-issue-ID outcome QUEUE (consumed in program
// order) cannot deterministically say which axis got which programmed
// result. axisRoutingAgent instead inspects each incoming AgentRequest's
// Policy.Notes for a per-axis marker string (the axis rubric's own title,
// which buildPolicyNotes always prepends) and returns whatever was
// programmed for that marker, so each axis deterministically gets its
// intended envelope regardless of goroutine scheduling. Safe for concurrent
// use.
type axisRoutingAgent struct {
	mu          sync.Mutex
	programmed  map[string]agent.AgentResult
	errs        map[string]error
	flaky       map[string]*flakyProgram
	invocations []agent.AgentRequest
}

// flakyProgram makes one axis marker fail its first failTimes Execute calls
// (returning err) before succeeding with thenResult on every subsequent
// call — used to test axisMaxAttempts' in-place retry (issue #161): a
// transient axis failure that recovers within the retry budget must produce
// a normal (non-degraded) Result.
type flakyProgram struct {
	failTimes  int
	err        error
	thenResult agent.AgentResult
	calls      int
}

func newAxisRoutingAgent() *axisRoutingAgent {
	return &axisRoutingAgent{
		programmed: map[string]agent.AgentResult{},
		errs:       map[string]error{},
	}
}

// programEnvelope programs marker's axis to return an AgentResult whose
// Summary is envelope.
func (a *axisRoutingAgent) programEnvelope(marker, envelope string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.programmed[marker] = agent.AgentResult{Status: agent.StatusImplemented, Summary: envelope}
}

// programEnvelopeWithUsage programs marker's axis to return an AgentResult
// whose Summary is envelope and whose Usage is usage, for asserting
// Result.Envelopes' per-axis token usage (issue #162).
func (a *axisRoutingAgent) programEnvelopeWithUsage(marker, envelope string, usage *agent.TokenUsage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.programmed[marker] = agent.AgentResult{Status: agent.StatusImplemented, Summary: envelope, Usage: usage}
}

// programError programs marker's axis to return err instead of a result on
// every call (simulating a persistently unrecoverable axis).
func (a *axisRoutingAgent) programError(marker string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.errs[marker] = err
}

// programFlaky programs marker's axis to fail its first failTimes calls
// with err, then succeed with an AgentResult whose Summary is thenEnvelope
// on every call after that.
func (a *axisRoutingAgent) programFlaky(marker string, failTimes int, err error, thenEnvelope string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.flaky == nil {
		a.flaky = map[string]*flakyProgram{}
	}
	a.flaky[marker] = &flakyProgram{
		failTimes:  failTimes,
		err:        err,
		thenResult: agent.AgentResult{Status: agent.StatusImplemented, Summary: thenEnvelope},
	}
}

func (a *axisRoutingAgent) Execute(_ context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.invocations = append(a.invocations, req)

	if req.Transcript != nil {
		req.Transcript.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "reviewing the diff"})
	}

	for marker, flaky := range a.flaky {
		if strings.Contains(req.Policy.Notes, marker) {
			flaky.calls++
			if flaky.calls <= flaky.failTimes {
				return agent.AgentResult{}, flaky.err
			}
			return flaky.thenResult, nil
		}
	}
	for marker, err := range a.errs {
		if strings.Contains(req.Policy.Notes, marker) {
			return agent.AgentResult{}, err
		}
	}
	for marker, result := range a.programmed {
		if strings.Contains(req.Policy.Notes, marker) {
			return result, nil
		}
	}
	return agent.AgentResult{Status: agent.StatusImplemented, Summary: cleanEnvelope}, nil
}

func (a *axisRoutingAgent) Invocations() []agent.AgentRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]agent.AgentRequest, len(a.invocations))
	copy(out, a.invocations)
	return out
}

func newIssue() domain.Issue {
	return domain.Issue{ID: "42", Title: "Fix the thing", Body: "Do the thing correctly."}
}

// reviewWithBugsSummary runs a full three-axis Review where the quality and
// docs axes are clean and only the bugs axis returns summary, returning the
// routing agent (for invocation assertions), the Result, and the error.
func reviewWithBugsSummary(t *testing.T, summary string) (*axisRoutingAgent, review.Result, error) {
	t.Helper()
	fake := newAxisRoutingAgent()
	fake.programEnvelope(bugsAxisMarker, summary)
	reviewer := agentreviewer.New(fake, 0.7)
	result, err := reviewer.Review(context.Background(), review.Request{
		Diff:  "diff --git a/foo.go b/foo.go\n+bug here",
		Issue: newIssue(),
	})
	return fake, result, err
}

func TestReview_AllThreeAxesClean_Approved(t *testing.T) {
	fake, result, err := reviewWithBugsSummary(t, cleanEnvelope)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Verdict != review.VerdictApproved {
		t.Errorf("Verdict = %q, want %q", result.Verdict, review.VerdictApproved)
	}
	if len(result.Findings) != 0 {
		t.Errorf("Findings = %+v, want empty", result.Findings)
	}
	if got := len(fake.Invocations()); got != 3 {
		t.Fatalf("invocations = %d, want 3 (one per axis)", got)
	}
}

func TestReview_RunsAllThreeAxesConcurrently_ThreeInvocationsAgainstWorkspace(t *testing.T) {
	fake := newAxisRoutingAgent()
	reviewer := agentreviewer.New(fake, 0.7)

	_, err := reviewer.Review(context.Background(), review.Request{
		Diff:          "diff --git a/foo.go b/foo.go\n+bug here",
		Issue:         newIssue(),
		WorkspacePath: "/workspaces/issue-42",
	})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}

	invocations := fake.Invocations()
	if len(invocations) != 3 {
		t.Fatalf("invocations = %d, want 3", len(invocations))
	}

	seen := map[string]bool{}
	for _, inv := range invocations {
		if inv.WorkspacePath != "/workspaces/issue-42" {
			t.Errorf("AgentRequest.WorkspacePath = %q, want %q", inv.WorkspacePath, "/workspaces/issue-42")
		}
		switch {
		case strings.Contains(inv.Policy.Notes, bugsAxisMarker):
			seen["bugs"] = true
		case strings.Contains(inv.Policy.Notes, qualityAxisMarker):
			seen["quality"] = true
		case strings.Contains(inv.Policy.Notes, docsAxisMarker):
			seen["docs"] = true
		default:
			t.Errorf("invocation Policy.Notes matched no known axis marker: %q", inv.Policy.Notes)
		}
	}
	for _, inv := range invocations {
		if inv.Mode != agent.ModeReview {
			t.Errorf("axis invocation Mode = %q, want %q (issue #183: the backend must not enforce the implement-mode result schema on a review)", inv.Mode, agent.ModeReview)
		}
	}
	for _, axis := range []string{"bugs", "quality", "docs"} {
		if !seen[axis] {
			t.Errorf("axis %q was never invoked", axis)
		}
	}
}

func TestReview_HighConfidenceHighSeverity_ChangesRequiredWithEnrichedFeedback(t *testing.T) {
	_, result, err := reviewWithBugsSummary(t, highConfidenceEnvelope)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Verdict != review.VerdictChangesRequired {
		t.Fatalf("Verdict = %q, want %q", result.Verdict, review.VerdictChangesRequired)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("Findings = %+v, want 1 finding", result.Findings)
	}

	f := result.Findings[0]
	if f.Severity != review.SeverityError {
		t.Errorf("Severity = %q, want %q", f.Severity, review.SeverityError)
	}
	if f.Confidence != 0.9 {
		t.Errorf("Confidence = %v, want 0.9", f.Confidence)
	}
	if f.Axis != "bugs" {
		t.Errorf("Axis = %q, want %q", f.Axis, "bugs")
	}
	if f.File != "internal/foo/foo.go" || f.Line != 42 {
		t.Errorf("File/Line = %q:%d, want internal/foo/foo.go:42", f.File, f.Line)
	}
	if f.Remedy != "check err before dereferencing the result" {
		t.Errorf("Remedy = %q", f.Remedy)
	}
	if !strings.Contains(f.Message, "nil pointer dereference") || !strings.Contains(f.Message, "evidence:") {
		t.Errorf("Message = %q, want message and folded evidence", f.Message)
	}

	feedback := review.BuildFeedback(result.Findings)
	if len(feedback) != 1 {
		t.Fatalf("BuildFeedback() = %+v, want 1 feedback item", feedback)
	}
	if feedback[0].Source != agent.FeedbackSourceReview {
		t.Errorf("feedback Source = %q, want %q", feedback[0].Source, agent.FeedbackSourceReview)
	}
	if !strings.Contains(feedback[0].Message, "bugs") || !strings.Contains(feedback[0].Message, "remedy:") {
		t.Errorf("feedback Message = %q, want axis+remedy folded in", feedback[0].Message)
	}
}

func TestReview_HighConfidenceMedSeverity_ChangesRequiredWithFeedback(t *testing.T) {
	_, result, err := reviewWithBugsSummary(t, highConfidenceMedSeverityEnvelope)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Verdict != review.VerdictChangesRequired {
		t.Fatalf("Verdict = %q, want %q", result.Verdict, review.VerdictChangesRequired)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("Findings = %+v, want 1 finding", result.Findings)
	}

	f := result.Findings[0]
	if f.Severity != review.SeverityWarning {
		t.Errorf("Severity = %q, want %q", f.Severity, review.SeverityWarning)
	}
	if f.Confidence != 0.9 {
		t.Errorf("Confidence = %v, want 0.9", f.Confidence)
	}

	feedback := review.BuildFeedback(result.Findings)
	if len(feedback) != 1 {
		t.Fatalf("BuildFeedback() = %+v, want 1 feedback item", feedback)
	}
	if feedback[0].Source != agent.FeedbackSourceReview {
		t.Errorf("feedback Source = %q, want %q", feedback[0].Source, agent.FeedbackSourceReview)
	}
	if !strings.Contains(feedback[0].Message, "WARNING") || !strings.Contains(feedback[0].Message, "route MED-and-higher") {
		t.Errorf("feedback Message = %q, want MED finding details folded in", feedback[0].Message)
	}
}

func TestReview_HighSeverityBelowFloor_ApprovedWithAdvisoryFinding(t *testing.T) {
	_, result, err := reviewWithBugsSummary(t, lowConfidenceHighSeverityEnvelope)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Verdict != review.VerdictApproved {
		t.Fatalf("Verdict = %q, want %q (below-floor finding must not block)", result.Verdict, review.VerdictApproved)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("Findings = %+v, want the below-floor finding surfaced as advisory", result.Findings)
	}
	if result.Findings[0].Confidence != 0.4 {
		t.Errorf("Confidence = %v, want 0.4", result.Findings[0].Confidence)
	}
}

func TestReview_DifferentAxesReportOnSameChange_BothAppearNoDedup(t *testing.T) {
	fake := newAxisRoutingAgent()
	fake.programEnvelope(bugsAxisMarker, highConfidenceEnvelope)
	fake.programEnvelope(qualityAxisMarker, qualityEnvelope)
	reviewer := agentreviewer.New(fake, 0.7)

	result, err := reviewer.Review(context.Background(), review.Request{
		Diff:  "diff --git a/foo.go b/foo.go\n+bug here",
		Issue: newIssue(),
	})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Verdict != review.VerdictChangesRequired {
		t.Fatalf("Verdict = %q, want %q", result.Verdict, review.VerdictChangesRequired)
	}
	// Both axes reported a finding on internal/foo/foo.go:42; #159's simple
	// combine does not dedup across axes, so both must appear.
	if len(result.Findings) != 2 {
		t.Fatalf("Findings = %+v, want 2 (no cross-axis dedup)", result.Findings)
	}

	axesSeen := map[string]bool{}
	for _, f := range result.Findings {
		axesSeen[f.Axis] = true
		if f.File != "internal/foo/foo.go" || f.Line != 42 {
			t.Errorf("finding File/Line = %q:%d, want internal/foo/foo.go:42", f.File, f.Line)
		}
	}
	if !axesSeen["bugs"] || !axesSeen["quality"] {
		t.Errorf("axesSeen = %+v, want both bugs and quality represented", axesSeen)
	}
}

// TestReview_OneAxisUnrecoverable_CleanSurvivors_Inconclusive is issue
// #161's core degradation case: one axis (quality) errors on every attempt
// (persistently unrecoverable, exhausting axisMaxAttempts in-place
// retries), the other two axes are clean. Since no surviving axis produced
// a blocking finding, Review must never approve on this partial coverage —
// it returns VerdictInconclusive instead, with a Coverage record naming
// exactly which axis failed and why.
func TestReview_OneAxisUnrecoverable_CleanSurvivors_Inconclusive(t *testing.T) {
	fake := newAxisRoutingAgent()
	wantErr := errors.New("boom")
	fake.programError(qualityAxisMarker, wantErr)
	reviewer := agentreviewer.New(fake, 0.7)

	result, err := reviewer.Review(context.Background(), review.Request{
		Diff:  "diff",
		Issue: newIssue(),
	})
	if err != nil {
		t.Fatalf("Review() error = %v, want nil (axis failures degrade, never error the call)", err)
	}
	if result.Verdict != review.VerdictInconclusive {
		t.Fatalf("Verdict = %q, want %q", result.Verdict, review.VerdictInconclusive)
	}

	invocations := fake.Invocations()
	qualityAttempts := 0
	for _, inv := range invocations {
		if strings.Contains(inv.Policy.Notes, qualityAxisMarker) {
			qualityAttempts++
		}
	}
	if qualityAttempts != 2 {
		t.Errorf("quality axis attempts = %d, want 2 (axisMaxAttempts in-place retry)", qualityAttempts)
	}

	var qualityCoverage, bugsCoverage *review.AxisCoverage
	for i := range result.Coverage {
		switch result.Coverage[i].Axis {
		case "quality":
			qualityCoverage = &result.Coverage[i]
		case "bugs":
			bugsCoverage = &result.Coverage[i]
		}
	}
	if qualityCoverage == nil || qualityCoverage.Ran {
		t.Fatalf("quality Coverage = %+v, want Ran=false", qualityCoverage)
	}
	if !strings.Contains(qualityCoverage.Reason, "boom") {
		t.Errorf("quality Coverage.Reason = %q, want it to mention the underlying error", qualityCoverage.Reason)
	}
	if bugsCoverage == nil || !bugsCoverage.Ran {
		t.Fatalf("bugs Coverage = %+v, want Ran=true", bugsCoverage)
	}
}

// TestReview_OneAxisUnrecoverable_SurvivingBlocker_ChangesRequired asserts
// issue #161's other degradation branch: even with the docs axis
// unrecoverable, a surviving blocking finding (bugs, HIGH severity at/above
// the confidence floor) must still route to VerdictChangesRequired — a
// degraded Review never escalates past a real, actionable finding a healthy
// axis actually found.
func TestReview_OneAxisUnrecoverable_SurvivingBlocker_ChangesRequired(t *testing.T) {
	fake := newAxisRoutingAgent()
	fake.programEnvelope(bugsAxisMarker, highConfidenceEnvelope)
	fake.programError(docsAxisMarker, errors.New("idle-timeout"))
	reviewer := agentreviewer.New(fake, 0.7)

	result, err := reviewer.Review(context.Background(), review.Request{
		Diff:  "diff",
		Issue: newIssue(),
	})
	if err != nil {
		t.Fatalf("Review() error = %v, want nil", err)
	}
	if result.Verdict != review.VerdictChangesRequired {
		t.Fatalf("Verdict = %q, want %q", result.Verdict, review.VerdictChangesRequired)
	}
	if len(result.Findings) != 1 || result.Findings[0].Axis != "bugs" {
		t.Fatalf("Findings = %+v, want the surviving bugs axis's blocker", result.Findings)
	}
}

// TestReview_AxisRecoversWithinRetry_NormalApproved asserts a transient
// axis failure (errors once, then succeeds) recovers within
// axisMaxAttempts and produces an ordinary, non-degraded Result: all three
// axes clean, VerdictApproved, and Coverage reporting every axis as Ran.
func TestReview_AxisRecoversWithinRetry_NormalApproved(t *testing.T) {
	fake := newAxisRoutingAgent()
	fake.programFlaky(qualityAxisMarker, 1, errors.New("transient crash"), cleanEnvelope)
	reviewer := agentreviewer.New(fake, 0.7)

	result, err := reviewer.Review(context.Background(), review.Request{
		Diff:  "diff",
		Issue: newIssue(),
	})
	if err != nil {
		t.Fatalf("Review() error = %v, want nil", err)
	}
	if result.Verdict != review.VerdictApproved {
		t.Fatalf("Verdict = %q, want %q", result.Verdict, review.VerdictApproved)
	}
	if len(result.Coverage) != 3 {
		t.Fatalf("Coverage = %+v, want 3 entries", result.Coverage)
	}
	for _, c := range result.Coverage {
		if !c.Ran {
			t.Errorf("Coverage[%s].Ran = false, want true (recovered within retry)", c.Axis)
		}
	}

	qualityAttempts := 0
	for _, inv := range fake.Invocations() {
		if strings.Contains(inv.Policy.Notes, qualityAxisMarker) {
			qualityAttempts++
		}
	}
	if qualityAttempts != 2 {
		t.Errorf("quality axis attempts = %d, want 2 (1 failure + 1 successful retry)", qualityAttempts)
	}
}

// TestReview_AllThreeAxesUnrecoverable_Inconclusive: an extreme but
// coverage-honest edge case — every axis unrecoverable, no survivors at
// all. There is nothing to block on, so Review must still never approve;
// it returns VerdictInconclusive with an empty Coverage-Ran set rather than
// erroring or approving on zero evidence.
func TestReview_AllThreeAxesUnrecoverable_Inconclusive(t *testing.T) {
	fake := agent.NewFakeAgent()
	wantErr := errors.New("boom")
	fake.ProgramError("42", wantErr)
	reviewer := agentreviewer.New(fake, 0.7)

	result, err := reviewer.Review(context.Background(), review.Request{
		Diff:  "diff",
		Issue: newIssue(),
	})
	if err != nil {
		t.Fatalf("Review() error = %v, want nil", err)
	}
	if result.Verdict != review.VerdictInconclusive {
		t.Fatalf("Verdict = %q, want %q", result.Verdict, review.VerdictInconclusive)
	}
	for _, c := range result.Coverage {
		if c.Ran {
			t.Errorf("Coverage[%s].Ran = true, want false (all axes unrecoverable)", c.Axis)
		}
	}
}

// TestReview_PersistentlyMalformedEnvelope_Inconclusive: an axis that keeps
// emitting non-JSON across every retry attempt is unrecoverable exactly
// like an Execute error, and degrades the same way.
func TestReview_PersistentlyMalformedEnvelope_Inconclusive(t *testing.T) {
	_, result, err := reviewWithBugsSummary(t, "the axis agent forgot to emit JSON")
	if err != nil {
		t.Fatalf("Review() error = %v, want nil", err)
	}
	if result.Verdict != review.VerdictInconclusive {
		t.Fatalf("Verdict = %q, want %q", result.Verdict, review.VerdictInconclusive)
	}
}

func TestReview_InjectsRubricAndDiffIntoAgentPolicyNotes(t *testing.T) {
	fake := newAxisRoutingAgent()
	reviewer := agentreviewer.New(fake, 0.7)

	_, err := reviewer.Review(context.Background(), review.Request{
		Diff:  "diff --git a/x b/x\n+marker-diff-content",
		Issue: newIssue(),
	})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}

	invocations := fake.Invocations()
	if len(invocations) != 3 {
		t.Fatalf("invocations = %d, want 3", len(invocations))
	}
	for _, inv := range invocations {
		notes := inv.Policy.Notes
		if !strings.Contains(notes, "marker-diff-content") {
			t.Errorf("Policy.Notes missing the diff content: %q", notes)
		}
		if !strings.Contains(notes, "Fix the thing") {
			t.Errorf("Policy.Notes missing the Issue title: %q", notes)
		}
		if !strings.Contains(notes, "JSON") {
			t.Errorf("Policy.Notes missing the embedded rubric's JSON output contract: %q", notes)
		}
	}
}

func TestReview_PassesWorkspacePathThroughToAgentRequest(t *testing.T) {
	fake := newAxisRoutingAgent()
	reviewer := agentreviewer.New(fake, 0.7)

	_, err := reviewer.Review(context.Background(), review.Request{
		Diff:          "diff --git a/foo.go b/foo.go\n+bug here",
		Issue:         newIssue(),
		WorkspacePath: "/workspaces/issue-42",
	})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}

	invocations := fake.Invocations()
	if len(invocations) != 3 {
		t.Fatalf("invocations = %d, want 3", len(invocations))
	}
	for _, inv := range invocations {
		if got := inv.WorkspacePath; got != "/workspaces/issue-42" {
			t.Errorf("AgentRequest.WorkspacePath = %q, want %q", got, "/workspaces/issue-42")
		}
	}
}

func TestReview_DefaultConfidenceFloor_UsedWhenNonPositive(t *testing.T) {
	fake := newAxisRoutingAgent()
	fake.programEnvelope(bugsAxisMarker, highConfidenceEnvelope)
	reviewer := agentreviewer.New(fake, 0)

	result, err := reviewer.Review(context.Background(), review.Request{Diff: "d", Issue: newIssue()})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Verdict != review.VerdictChangesRequired {
		t.Errorf("Verdict = %q, want %q (default floor 0.7 <= 0.9 confidence)", result.Verdict, review.VerdictChangesRequired)
	}
}

// TestReview_PopulatesEnvelopesWithRawFindingsAndUsage is issue #162's core
// agentreviewer acceptance criterion: every axis that ran to completion
// surfaces its raw findings envelope and token usage on Result.Envelopes,
// independent of and prior to synthesis.
func TestReview_PopulatesEnvelopesWithRawFindingsAndUsage(t *testing.T) {
	fake := newAxisRoutingAgent()
	fake.programEnvelopeWithUsage(bugsAxisMarker, highConfidenceEnvelope, &agent.TokenUsage{InputTokens: 111, OutputTokens: 222})
	fake.programEnvelope(qualityAxisMarker, cleanEnvelope)
	reviewer := agentreviewer.New(fake, 0.7)

	result, err := reviewer.Review(context.Background(), review.Request{Diff: "d", Issue: newIssue()})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if len(result.Envelopes) != 3 {
		t.Fatalf("Envelopes = %+v, want 3 entries (one per axis)", result.Envelopes)
	}

	byAxis := map[string]review.AxisEnvelope{}
	for _, e := range result.Envelopes {
		byAxis[e.Axis] = e
	}

	bugs, ok := byAxis["bugs"]
	if !ok {
		t.Fatalf("no bugs envelope among %+v", result.Envelopes)
	}
	if len(bugs.Findings) != 1 {
		t.Fatalf("bugs.Findings = %+v, want 1", bugs.Findings)
	}
	want := review.AxisRawFinding{
		Severity:   "HIGH",
		Confidence: 0.9,
		File:       "internal/foo/foo.go",
		Line:       42,
		Message:    "nil pointer dereference on the error path",
		Evidence:   "foo() returns (nil, err) but the caller dereferences the result before checking err",
		Remedy:     "check err before dereferencing the result",
	}
	if bugs.Findings[0] != want {
		t.Errorf("bugs.Findings[0] = %+v, want %+v", bugs.Findings[0], want)
	}
	if bugs.Usage == nil || bugs.Usage.InputTokens != 111 || bugs.Usage.OutputTokens != 222 {
		t.Errorf("bugs.Usage = %+v, want &{111 222}", bugs.Usage)
	}

	quality, ok := byAxis["quality"]
	if !ok {
		t.Fatalf("no quality envelope among %+v", result.Envelopes)
	}
	if len(quality.Findings) != 0 {
		t.Errorf("quality.Findings = %+v, want empty", quality.Findings)
	}
	if quality.Usage != nil {
		t.Errorf("quality.Usage = %+v, want nil (fake programmed no usage)", quality.Usage)
	}
}

// TestReview_PopulatesEnvelopesWithAssurances is issue #182's agentreviewer
// acceptance criterion: an axis's parsed assurances (issue #176) surface on
// Result.Envelopes alongside its findings, for the engine to persist.
func TestReview_PopulatesEnvelopesWithAssurances(t *testing.T) {
	fake := newAxisRoutingAgent()
	fake.programEnvelope(bugsAxisMarker, `{"axis":"bugs","findings":[],"assurances":["error handling in Save is correct and complete"]}`)
	fake.programEnvelope(qualityAxisMarker, cleanEnvelope)
	reviewer := agentreviewer.New(fake, 0.7)

	result, err := reviewer.Review(context.Background(), review.Request{Diff: "d", Issue: newIssue()})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}

	byAxis := map[string]review.AxisEnvelope{}
	for _, e := range result.Envelopes {
		byAxis[e.Axis] = e
	}

	bugs, ok := byAxis["bugs"]
	if !ok {
		t.Fatalf("no bugs envelope among %+v", result.Envelopes)
	}
	if len(bugs.Assurances) != 1 || bugs.Assurances[0] != "error handling in Save is correct and complete" {
		t.Errorf("bugs.Assurances = %+v, want [\"error handling in Save is correct and complete\"]", bugs.Assurances)
	}

	quality, ok := byAxis["quality"]
	if !ok {
		t.Fatalf("no quality envelope among %+v", result.Envelopes)
	}
	if len(quality.Assurances) != 0 {
		t.Errorf("quality.Assurances = %+v, want empty (cleanEnvelope has no assurances)", quality.Assurances)
	}
}

// TestReview_UnrecoverableAxisExcludedFromEnvelopes ensures an axis that
// never produced a usable envelope (Coverage.Ran == false) does not appear
// in Result.Envelopes — there is no raw envelope to persist for it.
func TestReview_UnrecoverableAxisExcludedFromEnvelopes(t *testing.T) {
	fake := newAxisRoutingAgent()
	fake.programError(qualityAxisMarker, errors.New("boom"))
	reviewer := agentreviewer.New(fake, 0.7)

	result, err := reviewer.Review(context.Background(), review.Request{Diff: "d", Issue: newIssue()})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	for _, e := range result.Envelopes {
		if e.Axis == "quality" {
			t.Fatalf("Envelopes contains an entry for unrecoverable axis quality: %+v", result.Envelopes)
		}
	}
	if len(result.Envelopes) != 2 {
		t.Fatalf("Envelopes = %+v, want 2 entries (bugs, docs)", result.Envelopes)
	}
}

// TestReview_RubricOverride_UsedWhenSet verifies issue #162's rubric
// override: setting Reviewer.Rubrics.Bugs substitutes that text for the
// bugs axis's embedded rubric.md in the agent invocation's Policy.Notes,
// while quality/docs keep their embedded defaults.
func TestReview_RubricOverride_UsedWhenSet(t *testing.T) {
	fake := newAxisRoutingAgent()
	reviewer := agentreviewer.New(fake, 0.7)
	reviewer.Rubrics = agentreviewer.RubricOverrides{Bugs: "CUSTOM BUGS RUBRIC override-marker"}

	_, err := reviewer.Review(context.Background(), review.Request{Diff: "d", Issue: newIssue()})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}

	var bugsNotes, qualityNotes string
	for _, inv := range fake.Invocations() {
		switch {
		case strings.Contains(inv.Policy.Notes, "override-marker"):
			bugsNotes = inv.Policy.Notes
		case strings.Contains(inv.Policy.Notes, qualityAxisMarker):
			qualityNotes = inv.Policy.Notes
		}
	}
	if bugsNotes == "" {
		t.Fatal("no invocation used the overridden bugs rubric")
	}
	if strings.Contains(bugsNotes, bugsAxisMarker) {
		t.Errorf("overridden bugs invocation still contains the embedded rubric marker: %q", bugsNotes)
	}
	if qualityNotes == "" || !strings.Contains(qualityNotes, qualityAxisMarker) {
		t.Errorf("quality invocation should still use its embedded rubric, got Policy.Notes = %q", qualityNotes)
	}
}

// fakeTranscriptSink is an agent.TranscriptSink test double that records
// every Emitted event, safe for concurrent use across the axes' goroutines.
type fakeTranscriptSink struct {
	mu     sync.Mutex
	events []agent.TranscriptEvent
}

func (s *fakeTranscriptSink) Emit(event agent.TranscriptEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *fakeTranscriptSink) Events() []agent.TranscriptEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]agent.TranscriptEvent, len(s.events))
	copy(out, s.events)
	return out
}

// TestReview_WiresPerAxisTranscriptSink is issue #219's review-side check:
// Review must call req.TranscriptSinkFor once per axis and wire the
// returned sink into that axis's AgentRequest.Transcript, so each axis's
// transcript is captured exactly as the implementation Agent's is.
func TestReview_WiresPerAxisTranscriptSink(t *testing.T) {
	fake := newAxisRoutingAgent()
	fake.programEnvelope(bugsAxisMarker, cleanEnvelope)
	fake.programEnvelope(qualityAxisMarker, cleanEnvelope)
	fake.programEnvelope(docsAxisMarker, cleanEnvelope)
	reviewer := agentreviewer.New(fake, 0.7)

	var mu sync.Mutex
	sinks := map[string]*fakeTranscriptSink{}

	_, err := reviewer.Review(context.Background(), review.Request{
		Diff:  "d",
		Issue: newIssue(),
		TranscriptSinkFor: func(subagent string) agent.TranscriptSink {
			mu.Lock()
			defer mu.Unlock()
			sink := &fakeTranscriptSink{}
			sinks[subagent] = sink
			return sink
		},
	})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, axisName := range []string{"bugs", "quality", "docs"} {
		sink, ok := sinks[axisName]
		if !ok {
			t.Fatalf("TranscriptSinkFor was never called for axis %s; got sinks for %v", axisName, sinks)
		}
		events := sink.Events()
		if len(events) != 1 || events[0].Text != "reviewing the diff" {
			t.Fatalf("axis %s sink.Events() = %+v, want the agent's emitted event", axisName, events)
		}
	}
}

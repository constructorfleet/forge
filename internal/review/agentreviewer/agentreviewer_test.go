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
	invocations []agent.AgentRequest
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

// programError programs marker's axis to return err instead of a result.
func (a *axisRoutingAgent) programError(marker string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.errs[marker] = err
}

func (a *axisRoutingAgent) Execute(_ context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.invocations = append(a.invocations, req)

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

func TestReview_OneAxisErrors_PropagatesError(t *testing.T) {
	fake := newAxisRoutingAgent()
	wantErr := errors.New("boom")
	fake.programError(qualityAxisMarker, wantErr)
	reviewer := agentreviewer.New(fake, 0.7)

	_, err := reviewer.Review(context.Background(), review.Request{
		Diff:  "diff",
		Issue: newIssue(),
	})
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("Review() error = %v, want it to wrap %v", err, wantErr)
	}
}

func TestReview_AgentExecuteError_Propagates(t *testing.T) {
	fake := agent.NewFakeAgent()
	wantErr := errors.New("boom")
	fake.ProgramError("42", wantErr)
	reviewer := agentreviewer.New(fake, 0.7)

	_, err := reviewer.Review(context.Background(), review.Request{
		Diff:  "diff",
		Issue: newIssue(),
	})
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("Review() error = %v, want it to wrap %v", err, wantErr)
	}
}

func TestReview_MalformedEnvelope_ReturnsError(t *testing.T) {
	_, _, err := reviewWithBugsSummary(t, "the axis agent forgot to emit JSON")
	if err == nil {
		t.Fatal("Review() error = nil, want an error for a malformed envelope")
	}
}

func TestReview_EmptyEnvelope_ReturnsError(t *testing.T) {
	fake := agent.NewFakeAgent()
	fake.ProgramDefault(agent.AgentResult{Status: agent.StatusImplemented, Summary: ""})
	reviewer := agentreviewer.New(fake, 0.7)

	_, err := reviewer.Review(context.Background(), review.Request{Diff: "d", Issue: newIssue()})
	if err == nil {
		t.Fatal("Review() error = nil, want an error for an empty envelope")
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

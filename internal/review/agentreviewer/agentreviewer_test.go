package agentreviewer_test

import (
	"context"
	"errors"
	"strings"
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

func newIssue() domain.Issue {
	return domain.Issue{ID: "42", Title: "Fix the thing", Body: "Do the thing correctly."}
}

func reviewWith(t *testing.T, summary string) (review.Result, error) {
	t.Helper()
	fake := agent.NewFakeAgent()
	fake.ProgramDefault(agent.AgentResult{
		Status:  agent.StatusImplemented,
		Summary: summary,
	})
	reviewer := agentreviewer.New(fake, 0.7)
	return reviewer.Review(context.Background(), review.Request{
		Diff:  "diff --git a/foo.go b/foo.go\n+bug here",
		Issue: newIssue(),
	})
}

func TestReview_AllClean_Approved(t *testing.T) {
	result, err := reviewWith(t, cleanEnvelope)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Verdict != review.VerdictApproved {
		t.Errorf("Verdict = %q, want %q", result.Verdict, review.VerdictApproved)
	}
	if len(result.Findings) != 0 {
		t.Errorf("Findings = %+v, want empty", result.Findings)
	}
}

func TestReview_HighConfidenceHighSeverity_ChangesRequiredWithEnrichedFeedback(t *testing.T) {
	result, err := reviewWith(t, highConfidenceEnvelope)
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
	result, err := reviewWith(t, lowConfidenceHighSeverityEnvelope)
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

func TestReview_MalformedEnvelope_ReturnsError(t *testing.T) {
	_, err := reviewWith(t, "the axis agent forgot to emit JSON")
	if err == nil {
		t.Fatal("Review() error = nil, want an error for a malformed envelope")
	}
}

func TestReview_EmptyEnvelope_ReturnsError(t *testing.T) {
	_, err := reviewWith(t, "")
	if err == nil {
		t.Fatal("Review() error = nil, want an error for an empty envelope")
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

func TestReview_InjectsRubricAndDiffIntoAgentPolicyNotes(t *testing.T) {
	fake := agent.NewFakeAgent()
	fake.ProgramDefault(agent.AgentResult{Status: agent.StatusImplemented, Summary: cleanEnvelope})
	reviewer := agentreviewer.New(fake, 0.7)

	_, err := reviewer.Review(context.Background(), review.Request{
		Diff:  "diff --git a/x b/x\n+marker-diff-content",
		Issue: newIssue(),
	})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}

	invocations := fake.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("invocations = %d, want 1", len(invocations))
	}
	notes := invocations[0].Policy.Notes
	if !strings.Contains(notes, "marker-diff-content") {
		t.Errorf("Policy.Notes missing the diff content")
	}
	if !strings.Contains(notes, "Fix the thing") {
		t.Errorf("Policy.Notes missing the Issue title")
	}
	if !strings.Contains(notes, "JSON") {
		t.Errorf("Policy.Notes missing the embedded rubric's JSON output contract")
	}
}

func TestReview_DefaultConfidenceFloor_UsedWhenNonPositive(t *testing.T) {
	fake := agent.NewFakeAgent()
	fake.ProgramDefault(agent.AgentResult{Status: agent.StatusImplemented, Summary: highConfidenceEnvelope})
	reviewer := agentreviewer.New(fake, 0)

	result, err := reviewer.Review(context.Background(), review.Request{Diff: "d", Issue: newIssue()})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Verdict != review.VerdictChangesRequired {
		t.Errorf("Verdict = %q, want %q (default floor 0.7 <= 0.9 confidence)", result.Verdict, review.VerdictChangesRequired)
	}
}

package agentreviewer

import (
	"testing"

	"github.com/Teagan42/forge/internal/review"
)

func TestBuildResult_NoFindings_Approved(t *testing.T) {
	result := buildResult(envelope{Axis: "bugs"}, "bugs", 0.7)
	if result.Verdict != review.VerdictApproved {
		t.Errorf("Verdict = %q, want %q", result.Verdict, review.VerdictApproved)
	}
	if len(result.Findings) != 0 {
		t.Errorf("Findings = %+v, want empty", result.Findings)
	}
}

func TestBuildResult_HighSeverityAtFloor_ChangesRequired(t *testing.T) {
	env := envelope{Findings: []axisFinding{{Severity: "HIGH", Confidence: 0.7, Message: "m"}}}
	result := buildResult(env, "bugs", 0.7)
	if result.Verdict != review.VerdictChangesRequired {
		t.Errorf("Verdict = %q, want %q (confidence == floor should block)", result.Verdict, review.VerdictChangesRequired)
	}
}

func TestBuildResult_HighSeverityBelowFloor_ApprovedAdvisory(t *testing.T) {
	env := envelope{Findings: []axisFinding{{Severity: "HIGH", Confidence: 0.69, Message: "m"}}}
	result := buildResult(env, "bugs", 0.7)
	if result.Verdict != review.VerdictApproved {
		t.Errorf("Verdict = %q, want %q", result.Verdict, review.VerdictApproved)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("Findings = %+v, want 1 advisory finding", result.Findings)
	}
	if result.Findings[0].Severity != review.SeverityError {
		t.Errorf("Severity = %q, want %q even though advisory", result.Findings[0].Severity, review.SeverityError)
	}
}

func TestBuildResult_MedSeverityAtFloor_ChangesRequired(t *testing.T) {
	env := envelope{Findings: []axisFinding{{Severity: "MED", Confidence: 0.7, Message: "m"}}}
	result := buildResult(env, "bugs", 0.7)
	if result.Verdict != review.VerdictChangesRequired {
		t.Errorf("Verdict = %q, want %q (MED findings at the confidence floor must be addressed)", result.Verdict, review.VerdictChangesRequired)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("Findings = %+v, want 1", result.Findings)
	}
	if result.Findings[0].Severity != review.SeverityWarning {
		t.Errorf("Severity = %q, want %q", result.Findings[0].Severity, review.SeverityWarning)
	}
}

func TestBuildResult_LowSeverityAtFloor_ApprovedAdvisory(t *testing.T) {
	env := envelope{Findings: []axisFinding{
		{Severity: "LOW", Confidence: 1.0, Message: "l"},
	}}
	result := buildResult(env, "bugs", 0.1)
	if result.Verdict != review.VerdictApproved {
		t.Errorf("Verdict = %q, want %q", result.Verdict, review.VerdictApproved)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("Findings = %+v, want 1", result.Findings)
	}
	if result.Findings[0].Severity != review.SeverityInfo {
		t.Errorf("Severity = %q, want %q", result.Findings[0].Severity, review.SeverityInfo)
	}
}

func TestBuildResult_OneHighAboveFloorAmongOthers_ChangesRequired(t *testing.T) {
	env := envelope{Findings: []axisFinding{
		{Severity: "LOW", Confidence: 1.0, Message: "l"},
		{Severity: "HIGH", Confidence: 0.95, Message: "h"},
	}}
	result := buildResult(env, "bugs", 0.7)
	if result.Verdict != review.VerdictChangesRequired {
		t.Errorf("Verdict = %q, want %q", result.Verdict, review.VerdictChangesRequired)
	}
	if len(result.Findings) != 2 {
		t.Fatalf("Findings = %+v, want both findings surfaced", result.Findings)
	}
}

func TestBuildResult_SetsAxisToBugsOnEveryFinding(t *testing.T) {
	env := envelope{Findings: []axisFinding{{Severity: "LOW", Confidence: 0.1, Message: "m"}}}
	result := buildResult(env, "bugs", 0.7)
	if result.Findings[0].Axis != "bugs" {
		t.Errorf("Axis = %q, want %q", result.Findings[0].Axis, "bugs")
	}
}

func TestComposeMessage_FoldsEvidenceIn(t *testing.T) {
	got := composeMessage(axisFinding{Message: "msg", Evidence: "evi"})
	want := "msg (evidence: evi)"
	if got != want {
		t.Errorf("composeMessage() = %q, want %q", got, want)
	}
}

func TestComposeMessage_NoEvidence_MessageOnly(t *testing.T) {
	got := composeMessage(axisFinding{Message: "msg"})
	if got != "msg" {
		t.Errorf("composeMessage() = %q, want %q", got, "msg")
	}
}

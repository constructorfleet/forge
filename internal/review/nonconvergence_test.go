package review_test

import (
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/review"
)

func TestFindingSignature_SameFieldsSameSignature(t *testing.T) {
	a := review.Finding{Severity: review.SeverityError, Axis: "bugs", File: "main.go", Line: 12, Message: "still broken"}
	b := review.Finding{Severity: review.SeverityError, Axis: "bugs", File: "main.go", Line: 12, Message: "still broken"}
	if a.Signature() != b.Signature() {
		t.Errorf("Signature() differ for identical Findings: %q vs %q", a.Signature(), b.Signature())
	}
}

func TestFindingSignature_DifferentMessageDifferentSignature(t *testing.T) {
	a := review.Finding{Severity: review.SeverityError, Axis: "bugs", File: "main.go", Line: 12, Message: "still broken"}
	b := review.Finding{Severity: review.SeverityError, Axis: "bugs", File: "main.go", Line: 12, Message: "a different problem"}
	if a.Signature() == b.Signature() {
		t.Errorf("Signature() equal for Findings with different Message: %q", a.Signature())
	}
}

func TestNonConvergent_EmptyPreviousYieldsNone(t *testing.T) {
	current := []review.Finding{{Severity: review.SeverityError, Axis: "bugs", File: "main.go", Line: 1, Message: "still broken"}}
	if got := review.NonConvergent(current, nil); len(got) != 0 {
		t.Errorf("NonConvergent with empty previous = %+v, want none", got)
	}
}

func TestNonConvergent_RepeatedIdenticalFindingReturnsIt(t *testing.T) {
	finding := review.Finding{Severity: review.SeverityError, Axis: "bugs", File: "main.go", Line: 1, Message: "still broken"}
	previous := []review.Finding{finding}
	current := []review.Finding{finding}
	got := review.NonConvergent(current, previous)
	if len(got) != 1 || got[0].Message != "still broken" {
		t.Fatalf("NonConvergent = %+v, want [%+v]", got, finding)
	}
}

func TestNonConvergent_NewFindingIsNotFlagged(t *testing.T) {
	previous := []review.Finding{{Severity: review.SeverityError, Axis: "bugs", File: "main.go", Line: 1, Message: "still broken"}}
	current := []review.Finding{{Severity: review.SeverityError, Axis: "bugs", File: "other.go", Line: 2, Message: "a new problem"}}
	if got := review.NonConvergent(current, previous); len(got) != 0 {
		t.Errorf("NonConvergent = %+v, want none (finding is new, not repeated)", got)
	}
}

func TestApplyOverrides_NoOverridesReturnsAllAsRemaining(t *testing.T) {
	findings := []review.Finding{{Severity: review.SeverityError, Axis: "bugs", File: "main.go", Line: 1, Message: "still broken"}}
	remaining, overridden := review.ApplyOverrides(findings, nil)
	if len(remaining) != 1 || len(overridden) != 0 {
		t.Fatalf("ApplyOverrides = remaining %+v overridden %+v, want all remaining", remaining, overridden)
	}
}

func TestApplyOverrides_MatchingOverrideSuppressesFinding(t *testing.T) {
	finding := review.Finding{Severity: review.SeverityError, Axis: "bugs", File: "main.go", Line: 1, Message: "still broken"}
	overrides := []domain.ReviewOverride{{IssueID: "43", Signature: finding.Signature()}}
	remaining, overridden := review.ApplyOverrides([]review.Finding{finding}, overrides)
	if len(remaining) != 0 {
		t.Errorf("remaining = %+v, want none", remaining)
	}
	if len(overridden) != 1 || overridden[0].Message != "still broken" {
		t.Fatalf("overridden = %+v, want [%+v]", overridden, finding)
	}
}

func TestApplyOverrides_NonMatchingOverrideLeavesFindingRemaining(t *testing.T) {
	finding := review.Finding{Severity: review.SeverityError, Axis: "bugs", File: "main.go", Line: 1, Message: "still broken"}
	overrides := []domain.ReviewOverride{{IssueID: "43", Signature: "unrelated-signature"}}
	remaining, overridden := review.ApplyOverrides([]review.Finding{finding}, overrides)
	if len(remaining) != 1 {
		t.Errorf("remaining = %+v, want [%+v]", remaining, finding)
	}
	if len(overridden) != 0 {
		t.Errorf("overridden = %+v, want none", overridden)
	}
}

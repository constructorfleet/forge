package review_test

import (
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/review"
)

func TestBuildFeedback_OneFeedbackPerFindingWithReviewSource(t *testing.T) {
	findings := []review.Finding{
		{
			Severity: review.SeverityError,
			File:     "main.go",
			Line:     42,
			Message:  "unhandled error",
			Axis:     "bugs",
			Remedy:   "check and return the error from f()",
		},
		{
			Severity: review.SeverityWarning,
			Message:  "no anchored location",
			Axis:     "quality",
			Remedy:   "extract the duplicated block into a helper",
		},
	}

	got := review.BuildFeedback(findings)
	if len(got) != 2 {
		t.Fatalf("got %d Feedback, want 2", len(got))
	}
	for i, fb := range got {
		if fb.Source != agent.FeedbackSourceReview {
			t.Errorf("Feedback[%d].Source = %q, want %q", i, fb.Source, agent.FeedbackSourceReview)
		}
	}
	if !strings.Contains(got[0].Message, "bugs") || !strings.Contains(got[0].Message, "ERROR") ||
		!strings.Contains(got[0].Message, "main.go") || !strings.Contains(got[0].Message, "42") ||
		!strings.Contains(got[0].Message, "unhandled error") ||
		!strings.Contains(got[0].Message, "check and return the error from f()") {
		t.Errorf("Feedback[0].Message = %q, want it to contain axis, severity, file, line, message, and remedy", got[0].Message)
	}
	if !strings.Contains(got[1].Message, "quality") || !strings.Contains(got[1].Message, "WARNING") ||
		!strings.Contains(got[1].Message, "no anchored location") ||
		!strings.Contains(got[1].Message, "extract the duplicated block into a helper") {
		t.Errorf("Feedback[1].Message = %q, want it to contain axis, severity, message, and remedy even with no file/line", got[1].Message)
	}
}

func TestBuildFeedback_IncludesAdvisoryFindingsNotOnlyErrors(t *testing.T) {
	findings := []review.Finding{
		{Severity: review.SeverityInfo, Axis: "docs", Message: "missing doc comment", Remedy: "add a doc comment"},
	}

	got := review.BuildFeedback(findings)
	if len(got) != 1 {
		t.Fatalf("got %d Feedback for one advisory Finding, want 1", len(got))
	}
	if !strings.Contains(got[0].Message, "INFO") || !strings.Contains(got[0].Message, "docs") {
		t.Errorf("Feedback[0].Message = %q, want an advisory (INFO) Finding to still be included", got[0].Message)
	}
}

func TestBuildFeedback_EmptyFindingsReturnsEmptySlice(t *testing.T) {
	got := review.BuildFeedback(nil)
	if len(got) != 0 {
		t.Errorf("got %d Feedback for nil Findings, want 0", len(got))
	}
}

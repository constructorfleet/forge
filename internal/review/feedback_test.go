package review_test

import (
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/review"
)

func TestBuildFeedback_OneFeedbackPerFindingWithReviewSource(t *testing.T) {
	findings := []review.Finding{
		{Severity: review.SeverityError, File: "main.go", Line: 42, Message: "unhandled error"},
		{Severity: review.SeverityWarning, Message: "no anchored location"},
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
	if !strings.Contains(got[0].Message, "ERROR") || !strings.Contains(got[0].Message, "main.go") ||
		!strings.Contains(got[0].Message, "42") || !strings.Contains(got[0].Message, "unhandled error") {
		t.Errorf("Feedback[0].Message = %q, want it to contain severity, file, line, and message", got[0].Message)
	}
	if !strings.Contains(got[1].Message, "WARNING") || !strings.Contains(got[1].Message, "no anchored location") {
		t.Errorf("Feedback[1].Message = %q, want it to contain severity and message even with no file/line", got[1].Message)
	}
}

func TestBuildFeedback_EmptyFindingsReturnsEmptySlice(t *testing.T) {
	got := review.BuildFeedback(nil)
	if len(got) != 0 {
		t.Errorf("got %d Feedback for nil Findings, want 0", len(got))
	}
}

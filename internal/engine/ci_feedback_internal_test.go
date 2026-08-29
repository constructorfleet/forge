package engine

import (
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/storage"
)

func TestBuildCIFeedback_CheckKindUsesCheckFraming(t *testing.T) {
	feedback := buildCIFeedback(storage.CIRun{
		CheckName: "test-linux",
		Details:   "TestExecutionResume expected READY, got PENDING",
	})
	if feedback.Source != agent.FeedbackSourceCI {
		t.Fatalf("Source = %s, want CI", feedback.Source)
	}
	if !strings.Contains(feedback.Message, "CI check failed") || !strings.Contains(feedback.Message, "test-linux") {
		t.Fatalf("Message = %q, want it to name the failed check", feedback.Message)
	}
}

func TestBuildCIFeedback_ReviewKindNamesReviewer(t *testing.T) {
	feedback := buildCIFeedback(storage.CIRun{
		Kind:      storage.CIRunKindReview,
		CheckName: "bob",
		Details:   "please rename this function",
	})
	if feedback.Source != agent.FeedbackSourceCI {
		t.Fatalf("Source = %s, want CI", feedback.Source)
	}
	if !strings.Contains(feedback.Message, "Reviewer requested changes") ||
		!strings.Contains(feedback.Message, "bob") ||
		!strings.Contains(feedback.Message, "please rename this function") {
		t.Fatalf("Message = %q, want it to name the reviewer and their comment", feedback.Message)
	}
	if strings.Contains(feedback.Message, "CI check failed") {
		t.Fatalf("Message = %q, want no CI-check framing for a review-kind run", feedback.Message)
	}
}

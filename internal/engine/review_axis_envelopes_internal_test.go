package engine

import (
	"encoding/json"
	"testing"

	"github.com/Teagan42/forge/internal/review"
)

// TestReviewAxisEnvelopesForStorage_MarshalsFindingsAndAssurances is issue
// #182's engine acceptance criterion: an axis's Findings and Assurances are
// marshaled together into RawEnvelope as {"findings": [...], "assurances":
// [...]}, so a persisted review's assurances can round-trip through
// storage.
func TestReviewAxisEnvelopesForStorage_MarshalsFindingsAndAssurances(t *testing.T) {
	result := review.Result{
		Coverage: []review.AxisCoverage{
			{Axis: "bugs", Ran: true},
		},
		Envelopes: []review.AxisEnvelope{
			{
				Axis: "bugs",
				Findings: []review.AxisRawFinding{
					{Severity: "HIGH", Confidence: 0.9, File: "main.go", Line: 42, Message: "unhandled error"},
				},
				Assurances: []string{"input validation checked at every call site"},
			},
		},
	}

	envelopes, err := reviewAxisEnvelopesForStorage("issue-1", result)
	if err != nil {
		t.Fatalf("reviewAxisEnvelopesForStorage: %v", err)
	}
	if len(envelopes) != 1 {
		t.Fatalf("got %d envelopes, want 1", len(envelopes))
	}

	var decoded struct {
		Findings   []map[string]any `json:"findings"`
		Assurances []string         `json:"assurances"`
	}
	if err := json.Unmarshal([]byte(envelopes[0].RawEnvelope), &decoded); err != nil {
		t.Fatalf("unmarshal RawEnvelope: %v", err)
	}
	if len(decoded.Findings) != 1 || decoded.Findings[0]["Message"] != "unhandled error" {
		t.Errorf("decoded.Findings = %+v, want one finding with Message %q", decoded.Findings, "unhandled error")
	}
	want := []string{"input validation checked at every call site"}
	if len(decoded.Assurances) != 1 || decoded.Assurances[0] != want[0] {
		t.Errorf("decoded.Assurances = %v, want %v", decoded.Assurances, want)
	}
}

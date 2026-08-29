package decisiongraph_test

import (
	"testing"

	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/decisionresolution"
	"github.com/Teagan42/forge/internal/planning"
)

func TestApplyResolution_RecordsSectionsAndApproves(t *testing.T) {
	d := &planning.Artifact{
		Kind:        planning.KindDecision,
		DerivedFrom: []planning.DerivedFromEntry{{Kind: planning.KindGoal, ID: "goal", Revision: "goalrev"}},
		Sections:    []planning.Section{{Heading: "Question", Body: "Where does state live?"}},
	}

	res := decisionresolution.Result{
		Outcome:      "SQLite",
		Rationale:    "simplest for MVP",
		Consequences: "single-writer",
		Assumptions:  "low concurrency",
	}

	out := decisiongraph.ApplyResolution(d, res)

	if out.State != "resolved" {
		t.Errorf("State = %q, want resolved", out.State)
	}
	if !planning.Ready(out) {
		t.Error("ApplyResolution result is not Ready (expected auto-approved)")
	}
	if out.Revision != planning.ComputeRevision(out) {
		t.Error("Revision is stale relative to its own content")
	}

	want := map[string]string{
		"Question":     "Where does state live?",
		"Outcome":      "SQLite",
		"Rationale":    "simplest for MVP",
		"Consequences": "single-writer",
		"Assumptions":  "low concurrency",
	}
	if len(out.Sections) != len(want) {
		t.Fatalf("len(Sections) = %d, want %d (%+v)", len(out.Sections), len(want), out.Sections)
	}
	for _, s := range out.Sections {
		if want[s.Heading] != s.Body {
			t.Errorf("section %q body = %q, want %q", s.Heading, s.Body, want[s.Heading])
		}
	}

	hasGoal := false
	for _, dep := range out.DerivedFrom {
		if dep.Kind == planning.KindGoal && dep.ID == "goal" {
			hasGoal = true
		}
	}
	if !hasGoal {
		t.Error("ApplyResolution dropped the goal DerivedFrom entry")
	}

	// Original decision must not be mutated.
	if planning.Ready(d) {
		t.Error("ApplyResolution mutated the input Artifact in place")
	}
}

func TestApplyResolution_OmitsBlankSections(t *testing.T) {
	d := &planning.Artifact{
		Kind:     planning.KindDecision,
		Sections: []planning.Section{{Heading: "Question", Body: "?"}},
	}

	out := decisiongraph.ApplyResolution(d, decisionresolution.Result{Outcome: "SQLite"})

	for _, s := range out.Sections {
		if s.Heading == "Rationale" || s.Heading == "Consequences" || s.Heading == "Assumptions" {
			t.Errorf("unexpected blank section %q rendered", s.Heading)
		}
	}
}

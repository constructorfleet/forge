package specengine_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/specengine"
)

func TestSpecEngineGenerateSpec(t *testing.T) {
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Revision: "goal-rev",
		State:    "approved",
		Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}},
	}

	decisions := map[string]*planning.Artifact{
		"001-storage": {
			Kind:     planning.KindDecision,
			Revision: "dec-rev",
			State:    "resolved",
			Sections: []planning.Section{
				{Heading: "Question", Body: "Which storage?"},
				{Heading: "Outcome", Body: "SQLite"},
			},
		},
	}

	loader := &fakeLoaderForTest{
		goal:      goal,
		decisions: decisions,
	}

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("specification-generation", "```json\n"+`{
		"summary": "A widget builder using SQLite",
		"requirements": [
			{"id": "REQ-001", "description": "Widget must be buildable"},
			{"id": "REQ-002", "description": "Widget must be testable"}
		],
		"non_goals": ["Not building a gadget"],
		"decision_refs": ["001-storage"]
	}`+"\n```\n")

	engine := specengine.NewSpecEngine(backend)
	err := engine.GenerateSpec(context.Background(), "feature-1", loader)
	if err != nil {
		t.Fatalf("GenerateSpec failed: %v", err)
	}

	if loader.spec == nil {
		t.Fatal("spec not saved")
	}

	if loader.spec.Kind != planning.KindSpec {
		t.Errorf("spec kind = %s, want %s", loader.spec.Kind, planning.KindSpec)
	}

	foundContext := false
	foundRequirements := false
	foundNonGoals := false
	for _, s := range loader.spec.Sections {
		switch s.Heading {
		case "Context":
			foundContext = true
			if s.Body != "A widget builder using SQLite" {
				t.Errorf("Context body = %q, want %q", s.Body, "A widget builder using SQLite")
			}
		case "Requirements":
			foundRequirements = true
			if s.Body != "REQ-001: Widget must be buildable\nREQ-002: Widget must be testable\n" {
				t.Errorf("Requirements body = %q", s.Body)
			}
		case "Non-Goals":
			foundNonGoals = true
			if s.Body != "- Not building a gadget\n" {
				t.Errorf("Non-Goals body = %q", s.Body)
			}
		}
	}

	if !foundContext {
		t.Error("Context section not found")
	}
	if !foundRequirements {
		t.Error("Requirements section not found")
	}
	if !foundNonGoals {
		t.Error("Non-Goals section not found")
	}

	if len(loader.spec.DerivedFrom) != 3 {
		t.Errorf("derived_from has %d entries, want 3", len(loader.spec.DerivedFrom))
	}
}

type fakeLoaderForTest struct {
	goal      *planning.Artifact
	decisions map[string]*planning.Artifact
	spec      *planning.Artifact
}

func (f *fakeLoaderForTest) LoadGoal(ctx context.Context, featureID string) (*planning.Artifact, error) {
	if f.goal == nil {
		return nil, fmt.Errorf("goal not found")
	}
	return f.goal, nil
}

func (f *fakeLoaderForTest) LoadDecisions(ctx context.Context, featureID string) (map[string]*planning.Artifact, error) {
	return f.decisions, nil
}

func (f *fakeLoaderForTest) SaveSpec(ctx context.Context, featureID string, spec *planning.Artifact) error {
	f.spec = spec
	return nil
}

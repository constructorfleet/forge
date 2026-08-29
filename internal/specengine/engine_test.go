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
	backend.ProgramResult("specification-review", "```json\n"+`{
		"verdict": "APPROVED",
		"summary": "Specification is clear, complete, and well-structured",
		"findings": []
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

func TestSpecEngineGenerateSpec_ReviewChangesRequiredThenApproved(t *testing.T) {
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
	// First review returns CHANGES_REQUIRED
	backend.ProgramResult("specification-review", "```json\n"+`{
		"verdict": "CHANGES_REQUIRED",
		"summary": "Specification needs improvement",
		"findings": [{"severity": "WARNING", "file": "", "line": 0, "message": "Non-Goals section is too brief"}]
	}`+"\n```\n")
	// Second generation (repair) produces improved spec
	backend.ProgramResult("specification-generation", "```json\n"+`{
		"summary": "A widget builder using SQLite with improved non-goals",
		"requirements": [
			{"id": "REQ-001", "description": "Widget must be buildable"},
			{"id": "REQ-002", "description": "Widget must be testable"}
		],
		"non_goals": ["Not building a gadget", "Not building a doohickey"],
		"decision_refs": ["001-storage"]
	}`+"\n```\n")
	// Second review returns APPROVED
	backend.ProgramResult("specification-review", "```json\n"+`{
		"verdict": "APPROVED",
		"summary": "Specification is clear, complete, and well-structured",
		"findings": []
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

	// Verify the repaired spec was saved
	foundNonGoals := false
	for _, s := range loader.spec.Sections {
		if s.Heading == "Non-Goals" {
			foundNonGoals = true
			if s.Body != "- Not building a gadget\n- Not building a doohickey\n" {
				t.Errorf("Non-Goals body = %q, want improved version", s.Body)
			}
		}
	}
	if !foundNonGoals {
		t.Error("Non-Goals section not found")
	}
}

func TestSpecEngineGenerateSpec_ReviewBudgetExhausted(t *testing.T) {
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
	// All 3 reviews return CHANGES_REQUIRED (default ReviewRetryLimit = 3)
	backend.ProgramResult("specification-review", "```json\n"+`{
		"verdict": "CHANGES_REQUIRED",
		"summary": "Specification needs improvement",
		"findings": [{"severity": "WARNING", "file": "", "line": 0, "message": "Non-Goals section is too brief"}]
	}`+"\n```\n")
	backend.ProgramResult("specification-generation", "```json\n"+`{
		"summary": "A widget builder using SQLite v2",
		"requirements": [
			{"id": "REQ-001", "description": "Widget must be buildable"},
			{"id": "REQ-002", "description": "Widget must be testable"}
		],
		"non_goals": ["Not building a gadget"],
		"decision_refs": ["001-storage"]
	}`+"\n```\n")
	backend.ProgramResult("specification-review", "```json\n"+`{
		"verdict": "CHANGES_REQUIRED",
		"summary": "Specification still needs improvement",
		"findings": [{"severity": "WARNING", "file": "", "line": 0, "message": "Non-Goals section is too brief"}]
	}`+"\n```\n")
	backend.ProgramResult("specification-generation", "```json\n"+`{
		"summary": "A widget builder using SQLite v3",
		"requirements": [
			{"id": "REQ-001", "description": "Widget must be buildable"},
			{"id": "REQ-002", "description": "Widget must be testable"}
		],
		"non_goals": ["Not building a gadget"],
		"decision_refs": ["001-storage"]
	}`+"\n```\n")
	backend.ProgramResult("specification-review", "```json\n"+`{
		"verdict": "CHANGES_REQUIRED",
		"summary": "Specification still needs improvement",
		"findings": [{"severity": "WARNING", "file": "", "line": 0, "message": "Non-Goals section is too brief"}]
	}`+"\n```\n")

	engine := specengine.NewSpecEngine(backend)
	err := engine.GenerateSpec(context.Background(), "feature-1", loader)
	if err == nil {
		t.Fatal("expected error for exhausted review budget, got nil")
	}
	if loader.spec != nil {
		t.Error("spec should not be saved when budget exhausted")
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

func (f *fakeLoaderForTest) LoadSpec(ctx context.Context, featureID string) (*planning.Artifact, error) {
	return f.spec, nil
}

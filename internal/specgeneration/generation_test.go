package specgeneration

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
)

func makeTestPC() planningagent.PlanningContext {
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Revision: "goal-rev",
		Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}},
	}
	goal.Sections[0].Body = "Build a widget"

	decisions := []*planning.Artifact{
		{
			Kind:     planning.KindDecision,
			Revision: "dec-rev",
			Sections: []planning.Section{{Heading: "Question", Body: "Which storage?"}, {Heading: "Outcome", Body: "SQLite"}},
		},
	}

	artifacts := []planningagent.NamedArtifact{
		{ID: "goal", Artifact: goal},
		{ID: "001-storage", Artifact: decisions[0]},
	}

	pc, err := planningagent.Compile(agent.RepositoryContext{BaseRevision: "base-rev"}, artifacts, nil)
	if err != nil {
		panic(err)
	}
	return pc
}

func TestSpecificationGeneration(t *testing.T) {
	pc := makeTestPC()

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("specification-generation", `{
		"summary": "A widget builder",
		"requirements": [
			{"id": "REQ-001", "description": "Widget must be buildable"},
			{"id": "REQ-002", "description": "Widget must be testable"}
		],
		"non_goals": ["Not building a gadget"],
		"decision_refs": ["001-storage"]
	}`)

	res, err := Generate(context.Background(), backend, pc)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if res.Summary != "A widget builder" {
		t.Errorf("summary = %q, want %q", res.Summary, "A widget builder")
	}
	if len(res.Requirements) != 2 {
		t.Errorf("len(requirements) = %d, want 2", len(res.Requirements))
	}
	if res.Requirements[0].ID != "REQ-001" || res.Requirements[1].ID != "REQ-002" {
		t.Errorf("requirement IDs = %v, want [REQ-001 REQ-002]", res.Requirements)
	}
	if len(res.NonGoals) != 1 || res.NonGoals[0] != "Not building a gadget" {
		t.Errorf("non_goals = %v, want [Not building a gadget]", res.NonGoals)
	}
	if len(res.DecisionRefs) != 1 || res.DecisionRefs[0] != "001-storage" {
		t.Errorf("decision_refs = %v, want [001-storage]", res.DecisionRefs)
	}
}

func TestSpecificationGenerationValidation(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{
			name:    "blank_summary",
			json:    `{"summary":"","requirements":[{"id":"REQ-001","description":"desc"}],"non_goals":[],"decision_refs":[]}`,
			wantErr: true,
		},
		{
			name:    "empty_requirements",
			json:    `{"summary":"summary","requirements":[],"non_goals":[],"decision_refs":[]}`,
			wantErr: true,
		},
		{
			name:    "blank_non_goals",
			json:    `{"summary":"summary","requirements":[{"id":"REQ-001","description":"desc"}],"non_goals":[""],"decision_refs":[]}`,
			wantErr: true,
		},
		{
			name:    "wrong_requirement_ID_format",
			json:    `{"summary":"summary","requirements":[{"id":"REQ-1","description":"desc"}],"non_goals":[],"decision_refs":[]}`,
			wantErr: true,
		},
		{
			name:    "requirement_ID_out_of_sequence",
			json:    `{"summary":"summary","requirements":[{"id":"REQ-002","description":"desc"}],"non_goals":[],"decision_refs":[]}`,
			wantErr: true,
		},
		{
			name:    "requirement_missing_description",
			json:    `{"summary":"summary","requirements":[{"id":"REQ-001","description":""}],"non_goals":[],"decision_refs":[]}`,
			wantErr: true,
		},
		{
			name:    "valid",
			json:    `{"summary":"summary","requirements":[{"id":"REQ-001","description":"desc"}],"non_goals":["ng"],"decision_refs":["d"]}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc := makeTestPC()
			backend := planningagent.NewFakeBackend()
			backend.ProgramResult("specification-generation", tt.json)

			_, err := Generate(context.Background(), backend, pc)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

package ticketplan

import (
	"context"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
)

func makeTestTicketPlanPC() planningagent.PlanningContext {
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Revision: "goal-rev",
		Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}},
	}

	decisions := []*planning.Artifact{
		{
			Kind:     planning.KindDecision,
			Revision: "dec-rev",
			Sections: []planning.Section{{Heading: "Question", Body: "Which storage?"}, {Heading: "Outcome", Body: "SQLite"}},
		},
	}

	spec := &planning.Artifact{
		Kind:     planning.KindSpec,
		Revision: "spec-rev",
		Sections: []planning.Section{
			{Heading: "Context", Body: "A widget builder using SQLite"},
			{Heading: "Requirements", Body: "REQ-001: Widget must be buildable\nREQ-002: Widget must be testable"},
			{Heading: "Non-Goals", Body: "Not building a gadget"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindGoal, ID: "goal", Revision: "goal-rev"},
			{Kind: planning.KindDecision, ID: "001-storage", Revision: "dec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
	}

	artifacts := []planningagent.NamedArtifact{
		{ID: "goal", Artifact: goal},
		{ID: "001-storage", Artifact: decisions[0]},
		{ID: "spec", Artifact: spec},
	}

	pc, err := planningagent.Compile(agent.RepositoryContext{BaseRevision: "base-rev"}, artifacts, nil)
	if err != nil {
		panic(err)
	}
	return pc
}

func TestTicketPlanGeneration(t *testing.T) {
	pc := makeTestTicketPlanPC()

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("ticket-plan-generation", `{
		"tickets": [
			{
				"key": "TKT-001",
				"objective": "Implement widget builder core",
				"requirements": ["REQ-001"],
				"acceptance_criteria": ["Widget builds successfully", "All unit tests pass"],
				"dependencies": []
			},
			{
				"key": "TKT-002",
				"objective": "Add widget integration tests",
				"requirements": ["REQ-002"],
				"acceptance_criteria": ["Integration tests pass", "Coverage > 80%"],
				"dependencies": ["TKT-001"]
			}
		]
	}`)

	res, err := Generate(context.Background(), backend, pc)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if len(res.Tickets) != 2 {
		t.Fatalf("len(tickets) = %d, want 2", len(res.Tickets))
	}

	if res.Tickets[0].Key != "TKT-001" {
		t.Errorf("tickets[0].key = %q, want TKT-001", res.Tickets[0].Key)
	}
	if res.Tickets[0].Objective != "Implement widget builder core" {
		t.Errorf("tickets[0].objective = %q", res.Tickets[0].Objective)
	}
	if len(res.Tickets[0].Requirements) != 1 || res.Tickets[0].Requirements[0] != "REQ-001" {
		t.Errorf("tickets[0].requirements = %v, want [REQ-001]", res.Tickets[0].Requirements)
	}
	if len(res.Tickets[0].AcceptanceCriteria) != 2 {
		t.Errorf("tickets[0].acceptance_criteria = %v, want 2 items", res.Tickets[0].AcceptanceCriteria)
	}
	if len(res.Tickets[0].Dependencies) != 0 {
		t.Errorf("tickets[0].dependencies = %v, want empty", res.Tickets[0].Dependencies)
	}

	if res.Tickets[1].Key != "TKT-002" {
		t.Errorf("tickets[1].key = %q, want TKT-002", res.Tickets[1].Key)
	}
	if len(res.Tickets[1].Dependencies) != 1 || res.Tickets[1].Dependencies[0] != "TKT-001" {
		t.Errorf("tickets[1].dependencies = %v, want [TKT-001]", res.Tickets[1].Dependencies)
	}
}

func TestTicketPlanGenerationWithEstimates(t *testing.T) {
	pc := makeTestTicketPlanPC()

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("ticket-plan-generation", `{
		"tickets": [
			{
				"key": "TKT-001",
				"objective": "Implement widget builder core",
				"requirements": ["REQ-001"],
				"acceptance_criteria": ["Widget builds successfully", "All unit tests pass"],
				"dependencies": [],
				"estimate": {"size": "S"}
			},
			{
				"key": "TKT-002",
				"objective": "Add widget integration tests",
				"requirements": ["REQ-002"],
				"acceptance_criteria": ["Integration tests pass", "Coverage > 80%"],
				"dependencies": ["TKT-001"],
				"estimate": {"size": "L", "risk": "complex_refactor"}
			}
		]
	}`)

	res, err := Generate(context.Background(), backend, pc)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if len(res.Tickets) != 2 {
		t.Fatalf("len(tickets) = %d, want 2", len(res.Tickets))
	}

	if res.Tickets[0].Estimate == nil {
		t.Fatal("tickets[0].estimate is nil, want S")
	}
	if res.Tickets[0].Estimate.Size != "S" {
		t.Errorf("tickets[0].estimate.size = %q, want S", res.Tickets[0].Estimate.Size)
	}
	if res.Tickets[0].Estimate.Risk != "" {
		t.Errorf("tickets[0].estimate.risk = %q, want empty", res.Tickets[0].Estimate.Risk)
	}

	if res.Tickets[1].Estimate == nil {
		t.Fatal("tickets[1].estimate is nil, want L with risk")
	}
	if res.Tickets[1].Estimate.Size != "L" {
		t.Errorf("tickets[1].estimate.size = %q, want L", res.Tickets[1].Estimate.Size)
	}
	if res.Tickets[1].Estimate.Risk != "complex_refactor" {
		t.Errorf("tickets[1].estimate.risk = %q, want complex_refactor", res.Tickets[1].Estimate.Risk)
	}
}

func TestTicketPlanGenerationWithImplementationContext(t *testing.T) {
	pc := makeTestTicketPlanPC()

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("ticket-plan-generation", `{
		"tickets": [
			{
				"key": "TKT-001",
				"objective": "Implement widget builder core",
				"requirements": ["REQ-001"],
				"acceptance_criteria": ["Widget builds successfully"],
				"dependencies": [],
				"implementation_context": ["internal/widget/builder.go: extend Build() with the new option"]
			}
		]
	}`)

	res, err := Generate(context.Background(), backend, pc)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if len(res.Tickets[0].ImplementationContext) != 1 {
		t.Fatalf("implementation_context = %v, want 1 item", res.Tickets[0].ImplementationContext)
	}
	if res.Tickets[0].ImplementationContext[0] != "internal/widget/builder.go: extend Build() with the new option" {
		t.Errorf("implementation_context[0] = %q", res.Tickets[0].ImplementationContext[0])
	}
}

func TestTicketPlanGenerationWithKinds(t *testing.T) {
	pc := makeTestTicketPlanPC()

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("ticket-plan-generation", `{
		"tickets": [
			{
				"key": "TKT-001",
				"kind": "code",
				"objective": "Implement widget builder core",
				"requirements": ["REQ-001"],
				"acceptance_criteria": ["Widget builds successfully"],
				"dependencies": []
			},
			{
				"key": "TKT-002",
				"kind": "non-code",
				"objective": "Verify the tracker acceptance criterion is already documented",
				"requirements": ["REQ-002"],
				"acceptance_criteria": ["No repository diff is required"],
				"dependencies": []
			}
		]
	}`)

	res, err := Generate(context.Background(), backend, pc)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if res.Tickets[0].Kind != planning.TicketKindCode {
		t.Errorf("tickets[0].kind = %q, want %q", res.Tickets[0].Kind, planning.TicketKindCode)
	}
	if res.Tickets[1].Kind != planning.TicketKindNonCode {
		t.Errorf("tickets[1].kind = %q, want %q", res.Tickets[1].Kind, planning.TicketKindNonCode)
	}
}

func TestBuildTicketPlanGenerationPromptIncludesRepositoryStructure(t *testing.T) {
	repo := agent.RepositoryContext{
		BaseRevision:     "base-rev",
		ProjectStructure: "cmd/\ninternal/\ngo.mod",
		Languages:        []string{"Go", "JavaScript"},
	}
	pc, err := planningagent.Compile(repo, nil, nil)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	prompt := buildTicketPlanGenerationPrompt(ticketPlanGenerationRequest{Context: pc})

	if !strings.Contains(prompt, "cmd/\ninternal/\ngo.mod") {
		t.Errorf("prompt missing project structure: %q", prompt)
	}
	if !strings.Contains(prompt, "Go, JavaScript") {
		t.Errorf("prompt missing languages: %q", prompt)
	}
}

func TestBuildTicketPlanGenerationPromptRequestsTicketKind(t *testing.T) {
	pc := makeTestTicketPlanPC()

	prompt := buildTicketPlanGenerationPrompt(ticketPlanGenerationRequest{Context: pc})

	if !strings.Contains(prompt, "kind") || !strings.Contains(prompt, "non-code") || !strings.Contains(prompt, "ready-for-human") {
		t.Fatalf("prompt missing ticket kind guidance: %q", prompt)
	}
}

func TestBuildTicketPlanGenerationPromptExcludesNoCodeDeliverables(t *testing.T) {
	pc, err := planningagent.Compile(agent.RepositoryContext{BaseRevision: "base-rev"}, nil, nil)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	prompt := buildTicketPlanGenerationPrompt(ticketPlanGenerationRequest{Context: pc})

	for _, want := range []string{
		"Do not create executable tickets for verification-only outcomes",
		"tracker-only deliverables",
		"cannot produce a git diff",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q: %q", want, prompt)
		}
	}
}

func TestRenderTicketBodyIncludesImplementationContext(t *testing.T) {
	t.Run("with entries", func(t *testing.T) {
		body := RenderTicketBody(TicketGenResult{
			Key:                   "TKT-001",
			Objective:             "Do the thing",
			Requirements:          []string{"REQ-001"},
			AcceptanceCriteria:    []string{"It works"},
			ImplementationContext: []string{"internal/widget/builder.go: extend Build()"},
		})
		if !strings.Contains(body, "### Implementation Context\n- internal/widget/builder.go: extend Build()") {
			t.Errorf("body missing implementation context section: %q", body)
		}
	})

	t.Run("without entries", func(t *testing.T) {
		body := RenderTicketBody(TicketGenResult{
			Key:                "TKT-001",
			Objective:          "Do the thing",
			Requirements:       []string{"REQ-001"},
			AcceptanceCriteria: []string{"It works"},
		})
		if !strings.Contains(body, "### Implementation Context\nNone") {
			t.Errorf("body missing None placeholder: %q", body)
		}
	})
}

func TestTicketPlanGenerationValidation(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{
			name:    "empty_tickets",
			json:    `{"tickets":[]}`,
			wantErr: true,
		},
		{
			name:    "missing_key",
			json:    `{"tickets":[{"objective":"obj","requirements":["REQ-001"],"acceptance_criteria":["ac"],"dependencies":[]}]}`,
			wantErr: true,
		},
		{
			name:    "invalid_key_format",
			json:    `{"tickets":[{"key":"TKT-1","objective":"obj","requirements":["REQ-001"],"acceptance_criteria":["ac"],"dependencies":[]}]}`,
			wantErr: true,
		},
		{
			name:    "key_out_of_sequence",
			json:    `{"tickets":[{"key":"TKT-002","objective":"obj","requirements":["REQ-001"],"acceptance_criteria":["ac"],"dependencies":[]}]}`,
			wantErr: true,
		},
		{
			name:    "missing_objective",
			json:    `{"tickets":[{"key":"TKT-001","requirements":["REQ-001"],"acceptance_criteria":["ac"],"dependencies":[]}]}`,
			wantErr: true,
		},
		{
			name:    "empty_objective",
			json:    `{"tickets":[{"key":"TKT-001","objective":"","requirements":["REQ-001"],"acceptance_criteria":["ac"],"dependencies":[]}]}`,
			wantErr: true,
		},
		{
			name:    "missing_requirements",
			json:    `{"tickets":[{"key":"TKT-001","objective":"obj","acceptance_criteria":["ac"],"dependencies":[]}]}`,
			wantErr: true,
		},
		{
			name:    "empty_requirements",
			json:    `{"tickets":[{"key":"TKT-001","objective":"obj","requirements":[],"acceptance_criteria":["ac"],"dependencies":[]}]}`,
			wantErr: true,
		},
		{
			name:    "invalid_requirement_id",
			json:    `{"tickets":[{"key":"TKT-001","objective":"obj","requirements":["REQ-1"],"acceptance_criteria":["ac"],"dependencies":[]}]}`,
			wantErr: true,
		},
		{
			name:    "missing_acceptance_criteria",
			json:    `{"tickets":[{"key":"TKT-001","objective":"obj","requirements":["REQ-001"],"dependencies":[]}]}`,
			wantErr: true,
		},
		{
			name:    "empty_acceptance_criteria",
			json:    `{"tickets":[{"key":"TKT-001","objective":"obj","requirements":["REQ-001"],"acceptance_criteria":[],"dependencies":[]}]}`,
			wantErr: true,
		},
		{
			name:    "invalid_dependency_format",
			json:    `{"tickets":[{"key":"TKT-001","objective":"obj","requirements":["REQ-001"],"acceptance_criteria":["ac"],"dependencies":["001-storage"]}]}`,
			wantErr: true,
		},
		{
			name:    "self_dependency",
			json:    `{"tickets":[{"key":"TKT-001","objective":"obj","requirements":["REQ-001"],"acceptance_criteria":["ac"],"dependencies":["TKT-001"]}]}`,
			wantErr: true,
		},
		{
			name:    "valid",
			json:    `{"tickets":[{"key":"TKT-001","objective":"obj","requirements":["REQ-001"],"acceptance_criteria":["ac"],"dependencies":[]},{"key":"TKT-002","objective":"obj2","requirements":["REQ-002"],"acceptance_criteria":["ac2"],"dependencies":["TKT-001"]}]}`,
			wantErr: false,
		},
		{
			name:    "valid_with_estimates",
			json:    `{"tickets":[{"key":"TKT-001","objective":"obj","requirements":["REQ-001"],"acceptance_criteria":["ac"],"dependencies":[],"estimate":{"size":"S"}},{"key":"TKT-002","objective":"obj2","requirements":["REQ-002"],"acceptance_criteria":["ac2"],"dependencies":["TKT-001"],"estimate":{"size":"M","risk":"new_tech"}}]}`,
			wantErr: false,
		},
		{
			name:    "invalid_estimate_size",
			json:    `{"tickets":[{"key":"TKT-001","objective":"obj","requirements":["REQ-001"],"acceptance_criteria":["ac"],"dependencies":[],"estimate":{"size":"XXL"}}]}`,
			wantErr: true,
		},
		{
			name:    "empty_estimate_size",
			json:    `{"tickets":[{"key":"TKT-001","objective":"obj","requirements":["REQ-001"],"acceptance_criteria":["ac"],"dependencies":[],"estimate":{"size":""}}]}`,
			wantErr: true,
		},
		{
			name:    "valid_with_implementation_context",
			json:    `{"tickets":[{"key":"TKT-001","objective":"obj","requirements":["REQ-001"],"acceptance_criteria":["ac"],"dependencies":[],"implementation_context":["internal/widget/builder.go: extend Build()"]}]}`,
			wantErr: false,
		},
		{
			name:    "empty_implementation_context_entry",
			json:    `{"tickets":[{"key":"TKT-001","objective":"obj","requirements":["REQ-001"],"acceptance_criteria":["ac"],"dependencies":[],"implementation_context":[""]}]}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc := makeTestTicketPlanPC()
			backend := planningagent.NewFakeBackend()
			backend.ProgramResult("ticket-plan-generation", tt.json)

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

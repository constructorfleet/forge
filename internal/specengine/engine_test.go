package specengine_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
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
	backend.ProgramResult("specification-generation", `{
		"summary": "A widget builder using SQLite",
		"requirements": [
			{"id": "REQ-001", "description": "Widget must be buildable"},
			{"id": "REQ-002", "description": "Widget must be testable"}
		],
		"non_goals": ["Not building a gadget"],
		"decision_refs": ["001-storage"]
	}`)
	backend.ProgramResult("specification-review", `{
		"verdict": "APPROVED",
		"summary": "Specification is clear, complete, and well-structured",
		"findings": []
	}`)

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
	backend.ProgramResult("specification-generation", `{
		"summary": "A widget builder using SQLite",
		"requirements": [
			{"id": "REQ-001", "description": "Widget must be buildable"},
			{"id": "REQ-002", "description": "Widget must be testable"}
		],
		"non_goals": ["Not building a gadget"],
		"decision_refs": ["001-storage"]
	}`)
	// First review returns CHANGES_REQUIRED
	backend.ProgramResult("specification-review", `{
		"verdict": "CHANGES_REQUIRED",
		"summary": "Specification needs improvement",
		"findings": [{"severity": "WARNING", "file": "", "line": 0, "message": "Non-Goals section is too brief"}]
	}`)
	// Second generation (repair) produces improved spec
	backend.ProgramResult("specification-generation", `{
		"summary": "A widget builder using SQLite with improved non-goals",
		"requirements": [
			{"id": "REQ-001", "description": "Widget must be buildable"},
			{"id": "REQ-002", "description": "Widget must be testable"}
		],
		"non_goals": ["Not building a gadget", "Not building a doohickey"],
		"decision_refs": ["001-storage"]
	}`)
	// Second review returns APPROVED
	backend.ProgramResult("specification-review", `{
		"verdict": "APPROVED",
		"summary": "Specification is clear, complete, and well-structured",
		"findings": []
	}`)

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
	backend.ProgramResult("specification-generation", `{
		"summary": "A widget builder using SQLite",
		"requirements": [
			{"id": "REQ-001", "description": "Widget must be buildable"},
			{"id": "REQ-002", "description": "Widget must be testable"}
		],
		"non_goals": ["Not building a gadget"],
		"decision_refs": ["001-storage"]
	}`)
	// All 3 reviews return CHANGES_REQUIRED (default ReviewRetryLimit = 3)
	backend.ProgramResult("specification-review", `{
		"verdict": "CHANGES_REQUIRED",
		"summary": "Specification needs improvement",
		"findings": [{"severity": "WARNING", "file": "", "line": 0, "message": "Non-Goals section is too brief"}]
	}`)
	backend.ProgramResult("specification-generation", `{
		"summary": "A widget builder using SQLite v2",
		"requirements": [
			{"id": "REQ-001", "description": "Widget must be buildable"},
			{"id": "REQ-002", "description": "Widget must be testable"}
		],
		"non_goals": ["Not building a gadget"],
		"decision_refs": ["001-storage"]
	}`)
	backend.ProgramResult("specification-review", `{
		"verdict": "CHANGES_REQUIRED",
		"summary": "Specification still needs improvement",
		"findings": [{"severity": "WARNING", "file": "", "line": 0, "message": "Non-Goals section is too brief"}]
	}`)
	backend.ProgramResult("specification-generation", `{
		"summary": "A widget builder using SQLite v3",
		"requirements": [
			{"id": "REQ-001", "description": "Widget must be buildable"},
			{"id": "REQ-002", "description": "Widget must be testable"}
		],
		"non_goals": ["Not building a gadget"],
		"decision_refs": ["001-storage"]
	}`)
	backend.ProgramResult("specification-review", `{
		"verdict": "CHANGES_REQUIRED",
		"summary": "Specification still needs improvement",
		"findings": [{"severity": "WARNING", "file": "", "line": 0, "message": "Non-Goals section is too brief"}]
	}`)

	engine := specengine.NewSpecEngine(backend)
	err := engine.GenerateSpec(context.Background(), "feature-1", loader)
	if err == nil {
		t.Fatal("expected error for exhausted review budget, got nil")
	}
	if !strings.Contains(err.Error(), "Non-Goals section is too brief") {
		t.Errorf("error should surface the recurring finding, got: %v", err)
	}
	if loader.spec != nil {
		t.Error("spec should not be saved when budget exhausted")
	}
}

// TestSpecEngineGenerateSpec_ReviewNoiseNotHardFailed is issue #249's
// reliability acceptance criterion: when the automated reviewer disagrees
// with itself across every retry (no finding recurs in every attempt), that
// is reviewer noise, not a genuine defect. The spec already passed
// deterministic validation, so budget exhaustion must not hard-fail the
// feature in this case.
func TestSpecEngineGenerateSpec_ReviewNoiseNotHardFailed(t *testing.T) {
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Revision: "goal-rev",
		State:    "approved",
		Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}},
	}

	loader := &fakeLoaderForTest{goal: goal}

	backend := planningagent.NewFakeBackend()
	genResult := func(v string) string {
		return fmt.Sprintf(`{
			"summary": "A widget builder %s",
			"requirements": [{"id": "REQ-001", "description": "Widget must be buildable"}],
			"non_goals": ["Not building a gadget"],
			"decision_refs": []
		}`, v)
	}
	reviewChangesRequired := func(msg string) string {
		return fmt.Sprintf(`{
			"verdict": "CHANGES_REQUIRED",
			"summary": "Specification needs improvement",
			"findings": [{"severity": "WARNING", "file": "", "line": 0, "message": %q}]
		}`, msg)
	}

	// Default ReviewRetryLimit is 3, which drives 4 review calls before
	// exhaustion (the 3 that trigger a repair, plus the one that observes
	// the budget is spent). Program each with a distinct finding so none
	// recurs across every attempt.
	backend.ProgramResult("specification-generation", genResult("v1"))
	backend.ProgramResult("specification-review", reviewChangesRequired("issue A"))
	backend.ProgramResult("specification-generation", genResult("v2"))
	backend.ProgramResult("specification-review", reviewChangesRequired("issue B"))
	backend.ProgramResult("specification-generation", genResult("v3"))
	backend.ProgramResult("specification-review", reviewChangesRequired("issue C"))
	backend.ProgramResult("specification-generation", genResult("v4"))
	backend.ProgramResult("specification-review", reviewChangesRequired("issue D"))

	engine := specengine.NewSpecEngine(backend)
	err := engine.GenerateSpec(context.Background(), "feature-1", loader)
	if err != nil {
		t.Fatalf("GenerateSpec should not hard-fail on reviewer noise: %v", err)
	}
	if loader.spec == nil {
		t.Fatal("spec should still be saved when the exhausted budget reflects reviewer noise, not a real defect")
	}
}

type fakeLoaderForTest struct {
	goal       *planning.Artifact
	decisions  map[string]*planning.Artifact
	spec       *planning.Artifact
	ticketPlan *planning.Artifact
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

func (f *fakeLoaderForTest) SaveTicketPlan(ctx context.Context, featureID string, tp *planning.Artifact) error {
	f.ticketPlan = tp
	return nil
}

func TestSpecEngineGenerateSpec_PropagatesRepositoryStructureToPrompt(t *testing.T) {
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Revision: "goal-rev",
		State:    "approved",
		Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}},
	}

	loader := &fakeLoaderForTest{goal: goal}

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("specification-generation", `{
		"summary": "A widget builder",
		"requirements": [{"id": "REQ-001", "description": "Widget must be buildable"}],
		"non_goals": [],
		"decision_refs": []
	}`)
	backend.ProgramResult("specification-review", `{
		"verdict": "APPROVED",
		"summary": "ok",
		"findings": []
	}`)

	engine := specengine.NewSpecEngine(backend)
	engine.Repository = agent.RepositoryContext{
		BaseRevision:     "base-rev",
		ProjectStructure: "cmd/\ninternal/\ngo.mod",
		Languages:        []string{"Go"},
	}

	if err := engine.GenerateSpec(context.Background(), "feature-1", loader); err != nil {
		t.Fatalf("GenerateSpec failed: %v", err)
	}

	found := false
	for _, inv := range backend.Invocations() {
		if inv.Key == "specification-generation" {
			found = true
			if !strings.Contains(inv.Prompt, "cmd/\ninternal/\ngo.mod") {
				t.Errorf("specification-generation prompt missing project structure: %q", inv.Prompt)
			}
		}
	}
	if !found {
		t.Fatal("specification-generation was never invoked")
	}
}

func TestSpecEngineGenerateTicketPlan_PropagatesRepositoryStructureToPrompt(t *testing.T) {
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Revision: "goal-rev",
		State:    "approved",
		Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}},
	}

	spec := &planning.Artifact{
		Kind:  planning.KindSpec,
		State: "approved",
		Sections: []planning.Section{
			{Heading: "Context", Body: "A widget builder"},
			{Heading: "Requirements", Body: "REQ-001: Widget must be buildable"},
			{Heading: "Non-Goals", Body: ""},
		},
	}
	spec.Revision = planning.ComputeRevision(spec)
	spec.ApprovedRevision = spec.Revision

	loader := &fakeLoaderForTest{goal: goal, spec: spec}

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("ticket-plan-generation", `{
		"tickets": [
			{
				"key": "TKT-001",
				"objective": "Implement widget builder core",
				"requirements": ["REQ-001"],
				"acceptance_criteria": ["Widget builds successfully"],
				"dependencies": []
			}
		]
	}`)
	backend.ProgramResult("ticket-plan-review", `{
		"verdict": "APPROVED",
		"summary": "ok",
		"findings": []
	}`)

	engine := specengine.NewSpecEngine(backend)
	engine.Repository = agent.RepositoryContext{
		BaseRevision:     "base-rev",
		ProjectStructure: "cmd/\ninternal/\ngo.mod",
		Languages:        []string{"Go"},
	}

	if err := engine.GenerateTicketPlan(context.Background(), "feature-1", loader); err != nil {
		t.Fatalf("GenerateTicketPlan failed: %v", err)
	}

	found := false
	for _, inv := range backend.Invocations() {
		if inv.Key == "ticket-plan-generation" {
			found = true
			if !strings.Contains(inv.Prompt, "cmd/\ninternal/\ngo.mod") {
				t.Errorf("ticket-plan-generation prompt missing project structure: %q", inv.Prompt)
			}
		}
	}
	if !found {
		t.Fatal("ticket-plan-generation was never invoked")
	}
}

func TestSpecEngineGenerateTicketPlan(t *testing.T) {
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

	// Create an approved spec
	spec := &planning.Artifact{
		Kind:  planning.KindSpec,
		State: "approved",
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
	spec.Revision = planning.ComputeRevision(spec)
	spec.ApprovedRevision = spec.Revision

	loader := &fakeLoaderForTest{
		goal:      goal,
		decisions: decisions,
		spec:      spec,
	}

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
	backend.ProgramResult("ticket-plan-review", `{
		"verdict": "APPROVED",
		"summary": "Ticket plan is well-structured with clear boundaries and full coverage",
		"findings": []
	}`)

	engine := specengine.NewSpecEngine(backend)
	err := engine.GenerateTicketPlan(context.Background(), "feature-1", loader)
	if err != nil {
		t.Fatalf("GenerateTicketPlan failed: %v", err)
	}

	if loader.ticketPlan == nil {
		t.Fatal("ticket plan not saved")
	}

	if loader.ticketPlan.Kind != planning.KindTicketPlan {
		t.Errorf("ticket plan kind = %s, want %s", loader.ticketPlan.Kind, planning.KindTicketPlan)
	}

	foundTickets := 0
	for _, s := range loader.ticketPlan.Sections {
		if len(s.Heading) > 7 && s.Heading[:7] == "Ticket:" {
			foundTickets++
			if s.Heading == "Ticket: TKT-001" {
				if !contains(s.Body, "Implement widget builder core") {
					t.Errorf("TKT-001 objective not found in body")
				}
				if !contains(s.Body, "REQ-001") {
					t.Errorf("TKT-001 requirement not found in body")
				}
				if !contains(s.Body, "Widget builds successfully") {
					t.Errorf("TKT-001 acceptance criteria not found in body")
				}
				if contains(s.Body, "Dependencies\nNone") == false && contains(s.Body, "Dependencies\nTKT-001") == false {
					t.Errorf("TKT-001 dependencies not found in body: %s", s.Body)
				}
			}
			if s.Heading == "Ticket: TKT-002" {
				if !contains(s.Body, "Add widget integration tests") {
					t.Errorf("TKT-002 objective not found in body")
				}
				if !contains(s.Body, "REQ-002") {
					t.Errorf("TKT-002 requirement not found in body")
				}
				if !contains(s.Body, "Integration tests pass") {
					t.Errorf("TKT-002 acceptance criteria not found in body")
				}
				if !contains(s.Body, "TKT-001") {
					t.Errorf("TKT-002 dependency on TKT-001 not found in body")
				}
			}
		}
	}

	if foundTickets != 2 {
		t.Errorf("found %d tickets, want 2", foundTickets)
	}

	// Check derived_from
	specFound := false
	repoFound := false
	for _, d := range loader.ticketPlan.DerivedFrom {
		if d.Kind == planning.KindSpec && d.ID == "spec" {
			specFound = true
			if d.Revision != spec.Revision {
				t.Errorf("derived_from spec revision = %s, want %s", d.Revision, spec.Revision)
			}
		}
		if d.Kind == "repository" && d.ID == "repository" {
			repoFound = true
		}
	}
	if !specFound {
		t.Error("derived_from spec entry not found")
	}
	if !repoFound {
		t.Error("derived_from repository entry not found")
	}
}

func TestSpecEngineGenerateTicketPlanRecordsTicketKinds(t *testing.T) {
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Revision: "goal-rev",
		State:    "approved",
		Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}},
	}
	spec := &planning.Artifact{
		Kind:  planning.KindSpec,
		State: "approved",
		Sections: []planning.Section{
			{Heading: "Context", Body: "A widget builder using SQLite"},
			{Heading: "Requirements", Body: "REQ-001: Widget must be buildable\nREQ-002: Widget must be verified manually"},
		},
	}
	spec.Revision = planning.ComputeRevision(spec)
	spec.ApprovedRevision = spec.Revision

	loader := &fakeLoaderForTest{goal: goal, spec: spec}
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
				"objective": "Verify tracker-only acceptance criterion",
				"requirements": ["REQ-002"],
				"acceptance_criteria": ["No repository diff is required"],
				"dependencies": []
			}
		]
	}`)
	backend.ProgramResult("ticket-plan-review", `{
		"verdict": "APPROVED",
		"summary": "ok",
		"findings": []
	}`)

	engine := specengine.NewSpecEngine(backend)
	if err := engine.GenerateTicketPlan(context.Background(), "feature-1", loader); err != nil {
		t.Fatalf("GenerateTicketPlan failed: %v", err)
	}

	if got := loader.ticketPlan.TicketKinds["TKT-001"]; got != planning.TicketKindCode {
		t.Errorf("ticket kind TKT-001 = %q, want %q", got, planning.TicketKindCode)
	}
	if got := loader.ticketPlan.TicketKinds["TKT-002"]; got != planning.TicketKindNonCode {
		t.Errorf("ticket kind TKT-002 = %q, want %q", got, planning.TicketKindNonCode)
	}
}

// TestSpecEngineGenerateTicketPlan_ReviewBudgetExhausted is issue #251's
// correctness case: when the automated ticketplanreview reviewer raises the
// same finding on every attempt, that is a recurring, reproducible defect
// and budget exhaustion must hard-fail with the recurring finding surfaced.
func TestSpecEngineGenerateTicketPlan_ReviewBudgetExhausted(t *testing.T) {
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Revision: "goal-rev",
		State:    "approved",
		Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}},
	}

	spec := &planning.Artifact{
		Kind:  planning.KindSpec,
		State: "approved",
		Sections: []planning.Section{
			{Heading: "Context", Body: "A widget builder using SQLite"},
			{Heading: "Requirements", Body: "REQ-001: Widget must be buildable"},
			{Heading: "Non-Goals", Body: "Not building a gadget"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindGoal, ID: "goal", Revision: "goal-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
	}
	spec.Revision = planning.ComputeRevision(spec)
	spec.ApprovedRevision = spec.Revision

	loader := &fakeLoaderForTest{goal: goal, spec: spec}

	backend := planningagent.NewFakeBackend()
	genResult := `{
		"tickets": [
			{
				"key": "TKT-001",
				"objective": "Implement widget builder core",
				"requirements": ["REQ-001"],
				"acceptance_criteria": ["Widget builds successfully"],
				"dependencies": []
			}
		]
	}`
	reviewChangesRequired := `{
		"verdict": "CHANGES_REQUIRED",
		"summary": "Ticket plan needs improvement",
		"findings": [{"severity": "WARNING", "ticket_key": "TKT-001", "message": "Acceptance criteria too thin"}]
	}`

	// All 3 reviews return the same CHANGES_REQUIRED finding (default
	// ReviewRetryLimit = 3), so it recurs across every attempt.
	backend.ProgramResult("ticket-plan-generation", genResult)
	backend.ProgramResult("ticket-plan-review", reviewChangesRequired)
	backend.ProgramResult("ticket-plan-generation", genResult)
	backend.ProgramResult("ticket-plan-review", reviewChangesRequired)
	backend.ProgramResult("ticket-plan-generation", genResult)
	backend.ProgramResult("ticket-plan-review", reviewChangesRequired)

	engine := specengine.NewSpecEngine(backend)
	err := engine.GenerateTicketPlan(context.Background(), "feature-1", loader)
	if err == nil {
		t.Fatal("expected error for exhausted review budget, got nil")
	}
	if !strings.Contains(err.Error(), "Acceptance criteria too thin") {
		t.Errorf("error should surface the recurring finding, got: %v", err)
	}
	if loader.ticketPlan != nil {
		t.Error("ticket plan should not be saved when budget exhausted")
	}
}

// TestSpecEngineGenerateTicketPlan_ReviewNoiseNotHardFailed mirrors issue
// #249's reliability fix for ticket-plan review: when the automated reviewer
// disagrees with itself across every retry (no finding recurs in every
// attempt), that is reviewer noise, not a genuine defect. The ticket plan
// already passed deterministic validation, so budget exhaustion must not
// hard-fail the feature in this case.
func TestSpecEngineGenerateTicketPlan_ReviewNoiseNotHardFailed(t *testing.T) {
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Revision: "goal-rev",
		State:    "approved",
		Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}},
	}

	spec := &planning.Artifact{
		Kind:  planning.KindSpec,
		State: "approved",
		Sections: []planning.Section{
			{Heading: "Context", Body: "A widget builder using SQLite"},
			{Heading: "Requirements", Body: "REQ-001: Widget must be buildable"},
			{Heading: "Non-Goals", Body: "Not building a gadget"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindGoal, ID: "goal", Revision: "goal-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
	}
	spec.Revision = planning.ComputeRevision(spec)
	spec.ApprovedRevision = spec.Revision

	loader := &fakeLoaderForTest{goal: goal, spec: spec}

	backend := planningagent.NewFakeBackend()
	genResult := func(v string) string {
		return fmt.Sprintf(`{
			"tickets": [
				{
					"key": "TKT-001",
					"objective": "Implement widget builder core %s",
					"requirements": ["REQ-001"],
					"acceptance_criteria": ["Widget builds successfully"],
					"dependencies": []
				}
			]
		}`, v)
	}
	reviewChangesRequired := func(msg string) string {
		return fmt.Sprintf(`{
			"verdict": "CHANGES_REQUIRED",
			"summary": "Ticket plan needs improvement",
			"findings": [{"severity": "WARNING", "ticket_key": "TKT-001", "message": %q}]
		}`, msg)
	}

	// Default ReviewRetryLimit is 3, which drives 4 review calls before
	// exhaustion (the 3 that trigger a repair, plus the one that observes
	// the budget is spent). Program each with a distinct finding so none
	// recurs across every attempt.
	backend.ProgramResult("ticket-plan-generation", genResult("v1"))
	backend.ProgramResult("ticket-plan-review", reviewChangesRequired("issue A"))
	backend.ProgramResult("ticket-plan-generation", genResult("v2"))
	backend.ProgramResult("ticket-plan-review", reviewChangesRequired("issue B"))
	backend.ProgramResult("ticket-plan-generation", genResult("v3"))
	backend.ProgramResult("ticket-plan-review", reviewChangesRequired("issue C"))
	backend.ProgramResult("ticket-plan-generation", genResult("v4"))
	backend.ProgramResult("ticket-plan-review", reviewChangesRequired("issue D"))

	engine := specengine.NewSpecEngine(backend)
	err := engine.GenerateTicketPlan(context.Background(), "feature-1", loader)
	if err != nil {
		t.Fatalf("GenerateTicketPlan should not hard-fail on reviewer noise: %v", err)
	}
	if loader.ticketPlan == nil {
		t.Fatal("ticket plan should still be saved when the exhausted budget reflects reviewer noise, not a real defect")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsInternal(s, substr)))
}

func containsInternal(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

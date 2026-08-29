package ticketplan

import (
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/planning"
)

func makeTestTicketPlan() *planning.Artifact {
	return &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nImplement widget builder\n\n### Requirements\nREQ-001\n\n### Acceptance Criteria\n- Widget builds successfully\n- Tests pass\n\n### Dependencies\nNone"},
			{Heading: "Ticket: TKT-002", Body: "### Objective\nAdd widget tests\n\n### Requirements\nREQ-002\n\n### Acceptance Criteria\n- All tests pass\n- Coverage > 80%\n\n### Dependencies\nTKT-001"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
	}
}

func TestTicketPlanParseImplementationContext(t *testing.T) {
	tp := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nImplement widget builder\n\n### Requirements\nREQ-001\n\n### Acceptance Criteria\n- Widget builds successfully\n\n### Implementation Context\n- internal/widget/builder.go: extend Build() with the new option\n- See internal/widget/analog_example.go for a similar pattern\n\n### Dependencies\nNone"},
		},
	}

	tickets, err := ParseTicketPlan(tp)
	if err != nil {
		t.Fatalf("ParseTicketPlan failed: %v", err)
	}
	if len(tickets) != 1 {
		t.Fatalf("expected 1 ticket, got %d", len(tickets))
	}

	want := []string{
		"internal/widget/builder.go: extend Build() with the new option",
		"See internal/widget/analog_example.go for a similar pattern",
	}
	got := tickets[0].ImplementationContext
	if len(got) != len(want) {
		t.Fatalf("expected %d implementation context entries, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTicketPlanParseImplementationContextOptional(t *testing.T) {
	tp := makeTestTicketPlan()

	tickets, err := ParseTicketPlan(tp)
	if err != nil {
		t.Fatalf("ParseTicketPlan failed: %v", err)
	}
	if len(tickets) != 2 {
		t.Fatalf("expected 2 tickets, got %d", len(tickets))
	}
	for _, ticket := range tickets {
		if len(ticket.ImplementationContext) != 0 {
			t.Errorf("ticket %s: expected no implementation context, got %v", ticket.Key, ticket.ImplementationContext)
		}
	}
}

func TestTicketPlanStructureValid(t *testing.T) {
	tp := makeTestTicketPlan()
	tp.Revision = planning.ComputeRevision(tp)

	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001", "REQ-002"}, "spec-rev", "repo-rev")
	if err != nil {
		t.Fatalf("ValidateTicketPlanDeterministic failed: %v", err)
	}
}

func TestTicketPlanStructureMissingTitle(t *testing.T) {
	tp := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "", Body: "### Objective\nImplement widget builder\n\n### Requirements\nREQ-001\n\n### Acceptance Criteria\n- Widget builds successfully\n\n### Dependencies\nNone"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
	}

	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001"}, "spec-rev", "repo-rev")
	if err == nil {
		t.Fatal("expected error for missing ticket title, got nil")
	}
}

func TestTicketPlanStructureMissingObjective(t *testing.T) {
	tp := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Requirements\nREQ-001\n\n### Acceptance Criteria\n- Widget builds successfully\n\n### Dependencies\nNone"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
	}

	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001"}, "spec-rev", "repo-rev")
	if err == nil {
		t.Fatal("expected error for missing objective, got nil")
	}
}

func TestTicketPlanStructureMissingAcceptanceCriteria(t *testing.T) {
	tp := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nImplement widget builder\n\n### Requirements\nREQ-001\n\n### Dependencies\nNone"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
	}

	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001"}, "spec-rev", "repo-rev")
	if err == nil {
		t.Fatal("expected error for missing acceptance criteria, got nil")
	}
}

func TestTicketPlanStructureDuplicateTempKeys(t *testing.T) {
	tp := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nImplement widget builder\n\n### Requirements\nREQ-001\n\n### Acceptance Criteria\n- Widget builds successfully\n\n### Dependencies\nNone"},
			{Heading: "Ticket: TKT-001", Body: "### Objective\nAdd widget tests\n\n### Requirements\nREQ-002\n\n### Acceptance Criteria\n- All tests pass\n\n### Dependencies\nTKT-001"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
	}

	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001", "REQ-002"}, "spec-rev", "repo-rev")
	if err == nil {
		t.Fatal("expected error for duplicate temp keys, got nil")
	}
}

func TestTicketPlanDependencyGraphCyclic(t *testing.T) {
	tp := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nImplement widget builder\n\n### Requirements\nREQ-001\n\n### Acceptance Criteria\n- Widget builds successfully\n\n### Dependencies\nTKT-002"},
			{Heading: "Ticket: TKT-002", Body: "### Objective\nAdd widget tests\n\n### Requirements\nREQ-002\n\n### Acceptance Criteria\n- All tests pass\n\n### Dependencies\nTKT-001"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
	}

	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001", "REQ-002"}, "spec-rev", "repo-rev")
	if err == nil {
		t.Fatal("expected error for cyclic dependency, got nil")
	}
}

func TestTicketPlanDependencyGraphUnresolvableTarget(t *testing.T) {
	tp := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nImplement widget builder\n\n### Requirements\nREQ-001\n\n### Acceptance Criteria\n- Widget builds successfully\n\n### Dependencies\nTKT-999"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
	}

	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001"}, "spec-rev", "repo-rev")
	if err == nil {
		t.Fatal("expected error for unresolvable dependency target, got nil")
	}
}

func TestTicketPlanDependencyOnDecision(t *testing.T) {
	tp := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nImplement widget builder\n\n### Requirements\nREQ-001\n\n### Acceptance Criteria\n- Widget builds successfully\n\n### Dependencies\n001-storage"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
	}

	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001"}, "spec-rev", "repo-rev")
	if err == nil {
		t.Fatal("expected error for dependency on decision, got nil")
	}
}

func TestTicketPlanSelfDependency(t *testing.T) {
	tp := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nImplement widget builder\n\n### Requirements\nREQ-001\n\n### Acceptance Criteria\n- Widget builds successfully\n\n### Dependencies\nTKT-001"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
	}

	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001"}, "spec-rev", "repo-rev")
	if err == nil {
		t.Fatal("expected error for self-dependency, got nil")
	}
}

func TestTicketPlanTraceabilityRequirementToTicket(t *testing.T) {
	tp := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nImplement widget builder\n\n### Requirements\nREQ-001\n\n### Acceptance Criteria\n- Widget builds successfully\n\n### Dependencies\nNone"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
	}

	// REQ-002 is in spec but not mapped to any ticket
	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001", "REQ-002"}, "spec-rev", "repo-rev")
	if err == nil {
		t.Fatal("expected error for unmapped requirement REQ-002, got nil")
	}
}

func TestTicketPlanTraceabilityTicketToRequirement(t *testing.T) {
	tp := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nImplement widget builder\n\n### Requirements\nREQ-999\n\n### Acceptance Criteria\n- Widget builds successfully\n\n### Dependencies\nNone"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
	}

	// REQ-999 is not in spec
	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001", "REQ-002"}, "spec-rev", "repo-rev")
	if err == nil {
		t.Fatal("expected error for invalid requirement reference REQ-999, got nil")
	}
}

func TestTicketPlanProvenanceSpecRevision(t *testing.T) {
	tp := makeTestTicketPlan()
	// Wrong spec revision in derived_from
	tp.DerivedFrom[0].Revision = "wrong-spec-rev"

	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001", "REQ-002"}, "spec-rev", "repo-rev")
	if err == nil {
		t.Fatal("expected error for spec revision mismatch, got nil")
	}
}

func TestTicketPlanProvenanceRepoRevision(t *testing.T) {
	tp := makeTestTicketPlan()
	// Wrong repo revision in derived_from
	tp.DerivedFrom[1].Revision = "wrong-repo-rev"

	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001", "REQ-002"}, "spec-rev", "repo-rev")
	if err == nil {
		t.Fatal("expected error for repo revision mismatch, got nil")
	}
}

func TestTicketPlanParseEstimatesFromMetadata(t *testing.T) {
	tp := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nImplement widget builder\n\n### Requirements\nREQ-001\n\n### Acceptance Criteria\n- Widget builds successfully\n\n### Dependencies\nNone"},
			{Heading: "Ticket: TKT-002", Body: "### Objective\nAdd widget tests\n\n### Requirements\nREQ-002\n\n### Acceptance Criteria\n- All tests pass\n- Coverage > 80%\n\n### Dependencies\nTKT-001"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
		Estimates: map[string]planning.TicketEstimate{
			"TKT-001": {Size: "S", Risk: "low"},
			"TKT-002": {Size: "XL", Risk: "complex_refactor"},
		},
	}
	tp.Revision = planning.ComputeRevision(tp)

	tickets, err := ParseTicketPlan(tp)
	if err != nil {
		t.Fatalf("ParseTicketPlan failed: %v", err)
	}

	if len(tickets) != 2 {
		t.Fatalf("len(tickets) = %d, want 2", len(tickets))
	}

	if tickets[0].Estimate == nil {
		t.Fatal("tickets[0].estimate is nil, want S")
	}
	if tickets[0].Estimate.Size != "S" {
		t.Errorf("tickets[0].estimate.size = %q, want S", tickets[0].Estimate.Size)
	}
	if tickets[0].Estimate.Risk != "low" {
		t.Errorf("tickets[0].estimate.risk = %q, want low", tickets[0].Estimate.Risk)
	}

	if tickets[1].Estimate == nil {
		t.Fatal("tickets[1].estimate is nil, want XL")
	}
	if tickets[1].Estimate.Size != "XL" {
		t.Errorf("tickets[1].estimate.size = %q, want XL", tickets[1].Estimate.Size)
	}
	if tickets[1].Estimate.Risk != "complex_refactor" {
		t.Errorf("tickets[1].estimate.risk = %q, want complex_refactor", tickets[1].Estimate.Risk)
	}
}

func TestTicketPlanParseWithoutEstimates(t *testing.T) {
	tp := makeTestTicketPlan()
	tp.Revision = planning.ComputeRevision(tp)

	tickets, err := ParseTicketPlan(tp)
	if err != nil {
		t.Fatalf("ParseTicketPlan failed: %v", err)
	}

	if len(tickets) != 2 {
		t.Fatalf("len(tickets) = %d, want 2", len(tickets))
	}

	if tickets[0].Estimate != nil {
		t.Errorf("tickets[0].estimate = %+v, want nil", tickets[0].Estimate)
	}
	if tickets[1].Estimate != nil {
		t.Errorf("tickets[1].estimate = %+v, want nil", tickets[1].Estimate)
	}
}

func TestTicketPlanValidationValidEstimates(t *testing.T) {
	tp := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nImplement widget builder\n\n### Requirements\nREQ-001\n\n### Acceptance Criteria\n- Widget builds successfully\n\n### Dependencies\nNone"},
			{Heading: "Ticket: TKT-002", Body: "### Objective\nAdd widget tests\n\n### Requirements\nREQ-002\n\n### Acceptance Criteria\n- All tests pass\n- Coverage > 80%\n\n### Dependencies\nTKT-001"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
		Estimates: map[string]planning.TicketEstimate{
			"TKT-001": {Size: "S"},
			"TKT-002": {Size: "M", Risk: "new_tech"},
			"TKT-003": {Size: "L"},
			"TKT-004": {Size: "XL", Risk: "unknown_deps"},
		},
	}
	tp.Revision = planning.ComputeRevision(tp)

	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001", "REQ-002"}, "spec-rev", "repo-rev")
	if err != nil {
		t.Fatalf("ValidateTicketPlanDeterministic failed with valid estimates: %v", err)
	}
}

func TestTicketPlanValidationInvalidEstimateSize(t *testing.T) {
	tp := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nImplement widget builder\n\n### Requirements\nREQ-001\n\n### Acceptance Criteria\n- Widget builds successfully\n\n### Dependencies\nNone"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
		Estimates: map[string]planning.TicketEstimate{
			"TKT-001": {Size: "XXL"},
		},
	}
	tp.Revision = planning.ComputeRevision(tp)

	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001"}, "spec-rev", "repo-rev")
	if err == nil {
		t.Fatal("expected error for invalid estimate size, got nil")
	}
	if !strings.Contains(err.Error(), "invalid estimate size") {
		t.Errorf("error message = %q, want to contain 'invalid estimate size'", err.Error())
	}
}

func TestTicketPlanValidationEmptyEstimateSize(t *testing.T) {
	tp := &planning.Artifact{
		Kind: planning.KindTicketPlan,
		Sections: []planning.Section{
			{Heading: "Ticket: TKT-001", Body: "### Objective\nImplement widget builder\n\n### Requirements\nREQ-001\n\n### Acceptance Criteria\n- Widget builds successfully\n\n### Dependencies\nNone"},
		},
		DerivedFrom: []planning.DerivedFromEntry{
			{Kind: planning.KindSpec, ID: "spec", Revision: "spec-rev"},
			{Kind: "repository", ID: "repository", Revision: "repo-rev"},
		},
		Estimates: map[string]planning.TicketEstimate{
			"TKT-001": {Size: ""},
		},
	}
	tp.Revision = planning.ComputeRevision(tp)

	err := ValidateTicketPlanDeterministic(tp, []string{"REQ-001"}, "spec-rev", "repo-rev")
	if err == nil {
		t.Fatal("expected error for empty estimate size, got nil")
	}
	if !strings.Contains(err.Error(), "empty size") {
		t.Errorf("error message = %q, want to contain 'empty size'", err.Error())
	}
}

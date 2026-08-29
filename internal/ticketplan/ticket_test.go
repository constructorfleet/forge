package ticketplan

import (
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

package planningagent_test

import (
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
)

func goalArtifact(body string) *planning.Artifact {
	return &planning.Artifact{
		Kind:     planning.KindGoal,
		Sections: []planning.Section{{Heading: "Goal", Body: body}},
	}
}

func decisionArtifact(body string) *planning.Artifact {
	return &planning.Artifact{
		Kind:     planning.KindDecision,
		Sections: []planning.Section{{Heading: "Answer", Body: body}},
	}
}

func TestCompile_ProjectsArtifactsIntoTypedViews(t *testing.T) {
	repo := agent.RepositoryContext{BaseRevision: "abc123"}
	artifacts := []planningagent.NamedArtifact{
		{ID: "goal", Artifact: goalArtifact("Ship the planning compiler.")},
		{ID: "002-durability", Artifact: decisionArtifact("Repo owns durable content.")},
		{ID: "001-domain", Artifact: decisionArtifact("Feature replaces Project.")},
	}

	pc, err := planningagent.Compile(repo, artifacts, map[string]string{"note": "from human"})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if pc.Goal == nil || pc.Goal.Sections["Goal"] != "Ship the planning compiler." {
		t.Errorf("Goal = %+v, want projected goal section", pc.Goal)
	}
	if len(pc.Decisions) != 2 {
		t.Fatalf("len(Decisions) = %d, want 2", len(pc.Decisions))
	}
	// Decisions are sorted by ID regardless of input order.
	if pc.Decisions[0].ID != "001-domain" || pc.Decisions[1].ID != "002-durability" {
		t.Errorf("Decisions order = [%s, %s], want sorted by ID", pc.Decisions[0].ID, pc.Decisions[1].ID)
	}
	if pc.HumanInputs["note"] != "from human" {
		t.Errorf("HumanInputs[note] = %q, want %q", pc.HumanInputs["note"], "from human")
	}
	if pc.ContextRevision == "" {
		t.Error("ContextRevision is empty, want a computed hash")
	}
}

func TestCompile_ContextRevisionStableAcrossReordering(t *testing.T) {
	repo := agent.RepositoryContext{BaseRevision: "abc123"}
	a := []planningagent.NamedArtifact{
		{ID: "goal", Artifact: goalArtifact("Ship it.")},
		{ID: "001-domain", Artifact: decisionArtifact("Feature replaces Project.")},
	}
	b := []planningagent.NamedArtifact{a[1], a[0]}

	pcA, err := planningagent.Compile(repo, a, nil)
	if err != nil {
		t.Fatalf("Compile a: %v", err)
	}
	pcB, err := planningagent.Compile(repo, b, nil)
	if err != nil {
		t.Fatalf("Compile b: %v", err)
	}
	if pcA.ContextRevision != pcB.ContextRevision {
		t.Errorf("ContextRevision differs by input order: %q vs %q", pcA.ContextRevision, pcB.ContextRevision)
	}
}

func TestCompile_ContextRevisionChangesWithArtifactContent(t *testing.T) {
	repo := agent.RepositoryContext{BaseRevision: "abc123"}
	before := []planningagent.NamedArtifact{{ID: "goal", Artifact: goalArtifact("Ship it.")}}
	after := []planningagent.NamedArtifact{{ID: "goal", Artifact: goalArtifact("Ship it differently.")}}

	pcBefore, err := planningagent.Compile(repo, before, nil)
	if err != nil {
		t.Fatalf("Compile before: %v", err)
	}
	pcAfter, err := planningagent.Compile(repo, after, nil)
	if err != nil {
		t.Fatalf("Compile after: %v", err)
	}
	if pcBefore.ContextRevision == pcAfter.ContextRevision {
		t.Error("ContextRevision unchanged despite artifact content change")
	}
}

func TestCompile_ContextRevisionChangesWithBaseRevision(t *testing.T) {
	artifacts := []planningagent.NamedArtifact{{ID: "goal", Artifact: goalArtifact("Ship it.")}}

	pcOld, err := planningagent.Compile(agent.RepositoryContext{BaseRevision: "old"}, artifacts, nil)
	if err != nil {
		t.Fatalf("Compile old: %v", err)
	}
	pcNew, err := planningagent.Compile(agent.RepositoryContext{BaseRevision: "new"}, artifacts, nil)
	if err != nil {
		t.Fatalf("Compile new: %v", err)
	}
	if pcOld.ContextRevision == pcNew.ContextRevision {
		t.Error("ContextRevision unchanged despite BaseRevision change")
	}
}

func TestCompile_RejectsDuplicateSingletonKind(t *testing.T) {
	repo := agent.RepositoryContext{BaseRevision: "abc123"}
	artifacts := []planningagent.NamedArtifact{
		{ID: "goal", Artifact: goalArtifact("First.")},
		{ID: "goal-2", Artifact: goalArtifact("Second.")},
	}
	if _, err := planningagent.Compile(repo, artifacts, nil); err == nil {
		t.Fatal("Compile: want error for duplicate goal artifact, got nil")
	}
}

func TestCompile_RejectsDuplicateID(t *testing.T) {
	repo := agent.RepositoryContext{BaseRevision: "abc123"}
	artifacts := []planningagent.NamedArtifact{
		{ID: "001-domain", Artifact: decisionArtifact("First.")},
		{ID: "001-domain", Artifact: decisionArtifact("Second.")},
	}
	if _, err := planningagent.Compile(repo, artifacts, nil); err == nil {
		t.Fatal("Compile: want error for duplicate ID, got nil")
	}
}

func TestCompile_RejectsNilArtifact(t *testing.T) {
	repo := agent.RepositoryContext{BaseRevision: "abc123"}
	artifacts := []planningagent.NamedArtifact{{ID: "goal", Artifact: nil}}
	if _, err := planningagent.Compile(repo, artifacts, nil); err == nil {
		t.Fatal("Compile: want error for nil artifact, got nil")
	}
}

func TestCompile_RejectsBlankID(t *testing.T) {
	repo := agent.RepositoryContext{BaseRevision: "abc123"}
	artifacts := []planningagent.NamedArtifact{{ID: "", Artifact: goalArtifact("x")}}
	if _, err := planningagent.Compile(repo, artifacts, nil); err == nil {
		t.Fatal("Compile: want error for blank ID, got nil")
	}
}

func TestCompile_HumanInputsIndependentOfCaller(t *testing.T) {
	repo := agent.RepositoryContext{BaseRevision: "abc123"}
	inputs := map[string]string{"k": "v"}
	pc, err := planningagent.Compile(repo, nil, inputs)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	inputs["k"] = "mutated"
	if pc.HumanInputs["k"] != "v" {
		t.Errorf("HumanInputs[k] = %q, want unaffected by caller mutation", pc.HumanInputs["k"])
	}
}

// TestCompileWithFacts_FactsParticipateInContextRevision pins the
// deliberate design call documented on PlanningContext.ContextRevision:
// implemented facts are compiled input, so a cache keyed on
// ContextRevision must not go stale-blind when new work lands.
func TestCompileWithFacts_FactsParticipateInContextRevision(t *testing.T) {
	repo := agent.RepositoryContext{BaseRevision: "abc123"}
	artifacts := []planningagent.NamedArtifact{{ID: "goal", Artifact: goalArtifact("Ship it.")}}

	bare, err := planningagent.Compile(repo, artifacts, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(bare.ImplementedFacts) != 0 {
		t.Errorf("Compile must attach no facts, got %+v", bare.ImplementedFacts)
	}

	facts := []planningagent.ImplementedFact{
		{IssueID: "2", Summary: "shipped storage", PlanRevision: "plan-rev-1"},
		{IssueID: "1", Summary: "shipped auth", PlanRevision: "plan-rev-1", Requirements: []string{"REQ-1"}},
	}
	withFacts, err := planningagent.CompileWithFacts(repo, artifacts, nil, facts)
	if err != nil {
		t.Fatalf("CompileWithFacts: %v", err)
	}
	if withFacts.ContextRevision == bare.ContextRevision {
		t.Fatal("implemented facts must change ContextRevision")
	}

	// Facts are normalized into a deterministic order, so caller ordering
	// never perturbs the cache key.
	if withFacts.ImplementedFacts[0].IssueID != "1" || withFacts.ImplementedFacts[1].IssueID != "2" {
		t.Errorf("facts were not sorted by issue ID: %+v", withFacts.ImplementedFacts)
	}
	reordered, err := planningagent.CompileWithFacts(repo, artifacts, nil, []planningagent.ImplementedFact{facts[1], facts[0]})
	if err != nil {
		t.Fatalf("CompileWithFacts: %v", err)
	}
	if reordered.ContextRevision != withFacts.ContextRevision {
		t.Error("ContextRevision must not depend on the order facts are supplied in")
	}

	// A fact recorded under a different plan revision is a different input.
	facts[0].PlanRevision = "plan-rev-2"
	moved, err := planningagent.CompileWithFacts(repo, artifacts, nil, facts)
	if err != nil {
		t.Fatalf("CompileWithFacts: %v", err)
	}
	if moved.ContextRevision == withFacts.ContextRevision {
		t.Error("a fact's plan revision must participate in ContextRevision")
	}
}

// TestCompileWithFacts_FactsIndependentOfCaller mirrors
// TestCompile_HumanInputsIndependentOfCaller.
func TestCompileWithFacts_FactsIndependentOfCaller(t *testing.T) {
	facts := []planningagent.ImplementedFact{{IssueID: "1", Summary: "shipped"}}
	pc, err := planningagent.CompileWithFacts(agent.RepositoryContext{}, nil, nil, facts)
	if err != nil {
		t.Fatalf("CompileWithFacts: %v", err)
	}
	facts[0].Summary = "mutated"
	if pc.ImplementedFacts[0].Summary != "shipped" {
		t.Errorf("ImplementedFacts[0].Summary = %q, want unaffected by caller mutation", pc.ImplementedFacts[0].Summary)
	}
}

package planninge2e_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/spec"
	"github.com/Teagan42/forge/internal/specengine"
)

// storageDecision is the resolved Decision most spec/ticket-plan scenarios
// compile against.
func storageDecision(goal *planning.Artifact) map[string]*planning.Artifact {
	return map[string]*planning.Artifact{
		"001-storage-engine": newResolvedDecision(
			"Which storage engine?", "SQLite",
			planning.DerivedFromEntry{Kind: planning.KindGoal, ID: "goal", Revision: goal.Revision},
		),
	}
}

// programApprovedSpec scripts a one-shot, immediately-approved
// SpecificationGeneration + SpecificationReview pair.
func programApprovedSpec(backend *planningagent.FakeBackend) {
	backend.ProgramResult("specification-generation", bareJSON(`{
		"summary": "A widget service backed by SQLite",
		"requirements": [
			{"id":"REQ-001","description":"Widgets persist across restarts"},
			{"id":"REQ-002","description":"Widgets are listable"}
		],
		"non_goals": ["No distributed storage"],
		"decision_refs": ["001-storage-engine"]
	}`))
	backend.ProgramResult("specification-review", bareJSON(`{"verdict":"APPROVED","summary":"clear","findings":[]}`))
}

// TestScenario05_SpecReviewRejectionAndRepair exercises the bounded spec
// repair loop: SpecificationReview returns CHANGES_REQUIRED once, the engine
// regenerates, the second review approves, and only the repaired spec is
// ever saved.
func TestScenario05_SpecReviewRejectionAndRepair(t *testing.T) {
	ctx := context.Background()
	goal := newGoal(goalBody)
	loader := newMemLoader(goal)
	loader.decisions = storageDecision(goal)

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("specification-generation", bareJSON(`{
		"summary": "A widget service",
		"requirements": [{"id":"REQ-001","description":"Widgets persist across restarts"}],
		"non_goals": ["No distributed storage"],
		"decision_refs": ["001-storage-engine"]
	}`))
	backend.ProgramResult("specification-review", bareJSON(`{
		"verdict":"CHANGES_REQUIRED",
		"summary":"the durability requirement is untestable as written",
		"findings":[{"severity":"ERROR","file":"","line":0,
			"message":"REQ-001 has no observable acceptance signal"}]
	}`))
	// The repair pass tightens REQ-001 and adds the missing requirement.
	backend.ProgramResult("specification-generation", bareJSON(`{
		"summary": "A widget service backed by SQLite",
		"requirements": [
			{"id":"REQ-001","description":"Widgets are readable after a process restart"},
			{"id":"REQ-002","description":"Widgets are listable"}
		],
		"non_goals": ["No distributed storage"],
		"decision_refs": ["001-storage-engine"]
	}`))
	backend.ProgramResult("specification-review", bareJSON(`{"verdict":"APPROVED","summary":"repaired","findings":[]}`))

	if err := specengine.NewSpecEngine(backend).GenerateSpec(ctx, "widget", loader); err != nil {
		t.Fatalf("GenerateSpec: %v", err)
	}

	if n := countKey(backend, "specification-generation"); n != 2 {
		t.Errorf("specification-generation ran %d times, want 2 (initial + one repair)", n)
	}
	if n := countKey(backend, "specification-review"); n != 2 {
		t.Errorf("specification-review ran %d times, want 2 (rejection + approval)", n)
	}

	saved := loader.spec
	if saved == nil {
		t.Fatal("no spec was saved")
	}
	// The saved spec is the repaired one, not the rejected draft.
	reqs := sectionBody(saved, "Requirements")
	if !strings.Contains(reqs, "REQ-001: Widgets are readable after a process restart") {
		t.Errorf("saved spec kept the rejected REQ-001 text:\n%s", reqs)
	}
	if !strings.Contains(reqs, "REQ-002: Widgets are listable") {
		t.Errorf("saved spec is missing the requirement the repair added:\n%s", reqs)
	}
	if planning.Stale(saved) {
		t.Error("the saved spec is Stale: its recorded revision does not match its content")
	}
	// Automated review approval is not human approval: the artifact is saved
	// unapproved and only `forge approve` binds a revision to it.
	if planning.Approved(saved) {
		t.Error("a review-approved spec must not be human-Approved() until it is explicitly approved")
	}
}

// TestScenario06_StaleSpecFromChangedDecision covers freshness's only
// mechanism: a Decision's content changing under a Spec that recorded its
// provenance. Nothing sets a staleness bit — the Spec goes stale purely
// because its recorded derived_from revision no longer matches the
// Decision's recomputed content revision.
func TestScenario06_StaleSpecFromChangedDecision(t *testing.T) {
	ctx := context.Background()
	goal := newGoal(goalBody)
	loader := newMemLoader(goal)
	loader.decisions = storageDecision(goal)
	decision := loader.decisions["001-storage-engine"]
	originalDecisionRev := decision.Revision

	backend := planningagent.NewFakeBackend()
	programApprovedSpec(backend)
	if err := specengine.NewSpecEngine(backend).GenerateSpec(ctx, "widget", loader); err != nil {
		t.Fatalf("GenerateSpec: %v", err)
	}
	specArtifact := loader.spec
	if specArtifact == nil {
		t.Fatal("no spec was saved")
	}
	approveArtifact(specArtifact)
	if !planning.Approved(specArtifact) {
		t.Fatal("the spec is not Approved() immediately after approval")
	}
	if rev, ok := derivedRevision(specArtifact, "001-storage-engine"); !ok || rev != originalDecisionRev {
		t.Fatalf("spec derived_from decision revision = %q (present=%v), want %q", rev, ok, originalDecisionRev)
	}

	beforePC, err := planningagent.Compile(repoCtx, []planningagent.NamedArtifact{
		{ID: "goal", Artifact: goal},
		{ID: "001-storage-engine", Artifact: decision},
	}, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// A human hand-edits the Decision's Outcome without recomputing its
	// revision, exactly as editing decisions/001-storage-engine.md would.
	decision.Sections[1].Body = "Postgres"

	if !planning.Stale(decision) {
		t.Error("a hand-edited Decision is not Stale()")
	}
	if planning.Approved(decision) {
		t.Error("editing a Decision's content must un-approve it automatically")
	}
	if planning.Ready(decision) {
		t.Error("an un-approved Decision must not be Ready()")
	}

	// The Spec's own content did not change, so it is neither Stale nor
	// un-Approved on its own terms: staleness here is a provenance fact.
	if planning.Stale(specArtifact) {
		t.Error("the spec's own content did not change, so it must not be Stale()")
	}
	if !planning.Approved(specArtifact) {
		t.Error("the spec's own approval binds its own content and must survive a Decision edit")
	}
	recorded, _ := derivedRevision(specArtifact, "001-storage-engine")
	if current := planning.ComputeRevision(decision); recorded == current {
		t.Fatalf("decision revision did not move on edit: still %q", current)
	}

	// The domain signal a caller actually gates on: revalidating the spec
	// against the Decision's current revision fails.
	err = spec.ValidateSpecDeterministic(
		&planning.Artifact{
			Kind:        specArtifact.Kind,
			Sections:    specArtifact.Sections,
			DerivedFrom: specArtifact.DerivedFrom,
		},
		loader.decisions,
		goal.Revision,
		map[string]string{"001-storage-engine": planning.ComputeRevision(decision)},
		mustDerivedRevision(t, specArtifact, "repository"),
	)
	if err == nil {
		t.Fatal("ValidateSpecDeterministic accepted a spec derived from a changed Decision")
	}
	if !strings.Contains(err.Error(), "decision 001-storage-engine revision mismatch") {
		t.Errorf("validation error = %v, want a decision revision mismatch", err)
	}

	// And the compiled PlanningContext cache key moves immediately on the
	// hand-edit, so nothing downstream can reuse a context computed before it.
	afterPC, err := planningagent.Compile(repoCtx, []planningagent.NamedArtifact{
		{ID: "goal", Artifact: goal},
		{ID: "001-storage-engine", Artifact: decision},
	}, nil)
	if err != nil {
		t.Fatalf("Compile after edit: %v", err)
	}
	if beforePC.ContextRevision == afterPC.ContextRevision {
		t.Error("PlanningContext.ContextRevision did not change after the Decision was hand-edited")
	}
}

func mustDerivedRevision(t *testing.T, a *planning.Artifact, id string) string {
	t.Helper()
	rev, ok := derivedRevision(a, id)
	if !ok {
		t.Fatalf("artifact has no derived_from entry for %q", id)
	}
	return rev
}

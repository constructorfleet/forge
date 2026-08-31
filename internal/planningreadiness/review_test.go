package planningreadiness_test

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/planningreadiness"
	"github.com/Teagan42/forge/internal/planningsurvey"
)

func goalArtifact() *planning.Artifact {
	g := &planning.Artifact{Kind: planning.KindGoal, Sections: []planning.Section{{Heading: "Goal", Body: "Ship a widget."}}}
	g.Revision = planning.ComputeRevision(g)
	return g
}

func questionDecision(question string, deps ...planning.DerivedFromEntry) *planning.Artifact {
	d := &planning.Artifact{
		Kind:        planning.KindDecision,
		DerivedFrom: deps,
		Sections:    []planning.Section{{Heading: "Question", Body: question}},
	}
	d.Revision = planning.ComputeRevision(d)
	return d
}

func makePlanningContext(goal *planning.Artifact, decisions map[string]*planning.Artifact) planningagent.PlanningContext {
	artifacts := make([]planningagent.NamedArtifact, 0, len(decisions)+1)
	if goal != nil {
		artifacts = append(artifacts, planningagent.NamedArtifact{ID: "goal", Artifact: goal})
	}
	for id, d := range decisions {
		artifacts = append(artifacts, planningagent.NamedArtifact{ID: id, Artifact: d})
	}
	repo := agent.RepositoryContext{BaseRevision: "base"}
	pc, _ := planningagent.Compile(repo, artifacts, nil)
	return pc
}

func TestReview_ReadyForSpec(t *testing.T) {
	goal := goalArtifact()

	d := questionDecision("Where does state live?")
	d.ApprovedRevision = d.Revision

	decisions := map[string]*planning.Artifact{"001-storage": d}

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("planning-readiness-review", `{"status":"READY_FOR_SPEC","decisions":[]}`)

	pc := makePlanningContext(goal, decisions)

	res, err := planningreadiness.Review(context.Background(), backend, pc)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if res.Status != planningreadiness.StatusReadyForSpec {
		t.Errorf("status = %q, want %q", res.Status, planningreadiness.StatusReadyForSpec)
	}
	if len(res.Decisions) != 0 {
		t.Errorf("decisions = %v, want empty", res.Decisions)
	}

	invocations := backend.Invocations()
	if len(invocations) != 1 || invocations[0].Key != "planning-readiness-review" {
		t.Fatalf("Invocations() = %+v, want exactly one planning-readiness-review invocation", invocations)
	}
}

func TestReview_NotReadyMaterializesDecisions(t *testing.T) {
	goal := goalArtifact()

	d := questionDecision("Where does state live?")
	d.ApprovedRevision = d.Revision

	decisions := map[string]*planning.Artifact{"001-storage": d}

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("planning-readiness-review", `{"status":"NOT_READY","decisions":[`+
		`{"temp_key":"a","title":"Pick auth","question":"Which auth strategy?","depends_on":[],"consequential":true}`+
		`]}`)

	pc := makePlanningContext(goal, decisions)

	res, err := planningreadiness.Review(context.Background(), backend, pc)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if res.Status != planningreadiness.StatusNotReady {
		t.Errorf("status = %q, want %q", res.Status, planningreadiness.StatusNotReady)
	}
	if len(res.Decisions) != 1 {
		t.Fatalf("len(decisions) = %d, want 1", len(res.Decisions))
	}
	if res.Decisions[0].TempKey != "a" {
		t.Errorf("temp_key = %q, want %q", res.Decisions[0].TempKey, "a")
	}
	if res.Decisions[0].Title != "Pick auth" {
		t.Errorf("title = %q, want %q", res.Decisions[0].Title, "Pick auth")
	}
	if res.Decisions[0].Question != "Which auth strategy?" {
		t.Errorf("question = %q, want %q", res.Decisions[0].Question, "Which auth strategy?")
	}
	if len(res.Decisions[0].DependsOn) != 0 {
		t.Errorf("depends_on = %v, want empty", res.Decisions[0].DependsOn)
	}
	if !res.Decisions[0].Consequential {
		t.Error("consequential = false, want true")
	}
}

func TestReview_NotReadyWithNoDecisionsErrors(t *testing.T) {
	goal := goalArtifact()

	d := questionDecision("Where does state live?")
	d.ApprovedRevision = d.Revision

	decisions := map[string]*planning.Artifact{"001-storage": d}

	backend := planningagent.NewFakeBackend()
	backend.ProgramDefault(`{"status":"NOT_READY","decisions":[]}`)

	pc := makePlanningContext(goal, decisions)

	_, err := planningreadiness.Review(context.Background(), backend, pc)
	if err == nil {
		t.Fatal("Review: want error for NOT_READY with no proposed decisions, got nil")
	}
}

func TestReview_ReadyForSpecWithDecisionsErrors(t *testing.T) {
	goal := goalArtifact()

	d := questionDecision("Where does state live?")
	d.ApprovedRevision = d.Revision

	decisions := map[string]*planning.Artifact{"001-storage": d}

	backend := planningagent.NewFakeBackend()
	backend.ProgramDefault(`{"status":"READY_FOR_SPEC","decisions":[{"temp_key":"a","title":"Pick auth","question":"Which?","depends_on":[],"consequential":true}]}`)

	pc := makePlanningContext(goal, decisions)

	_, err := planningreadiness.Review(context.Background(), backend, pc)
	if err == nil {
		t.Fatal("Review: want error for READY_FOR_SPEC with proposed decisions, got nil")
	}
}

func TestReview_InvalidStatusErrors(t *testing.T) {
	goal := goalArtifact()

	d := questionDecision("Where does state live?")
	d.ApprovedRevision = d.Revision

	decisions := map[string]*planning.Artifact{"001-storage": d}

	backend := planningagent.NewFakeBackend()
	backend.ProgramDefault(`{"status":"INVALID","decisions":[]}`)

	pc := makePlanningContext(goal, decisions)

	_, err := planningreadiness.Review(context.Background(), backend, pc)
	if err == nil {
		t.Fatal("Review: want error for invalid status, got nil")
	}
}

func TestReview_FreshInvocationEachTime(t *testing.T) {
	goal := goalArtifact()

	d := questionDecision("Where does state live?")
	d.ApprovedRevision = d.Revision

	decisions := map[string]*planning.Artifact{"001-storage": d}

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("planning-readiness-review", `{"status":"READY_FOR_SPEC","decisions":[]}`)
	backend.ProgramResult("planning-readiness-review", `{"status":"READY_FOR_SPEC","decisions":[]}`)

	pc := makePlanningContext(goal, decisions)

	// First invocation
	_, err := planningreadiness.Review(context.Background(), backend, pc)
	if err != nil {
		t.Fatalf("Review #1: %v", err)
	}

	// Second invocation - should be a fresh call
	_, err = planningreadiness.Review(context.Background(), backend, pc)
	if err != nil {
		t.Fatalf("Review #2: %v", err)
	}

	invocations := backend.Invocations()
	if len(invocations) != 2 {
		t.Fatalf("Invocations() len = %d, want 2 (fresh invocation each time)", len(invocations))
	}
	if invocations[0].Key != "planning-readiness-review" || invocations[1].Key != "planning-readiness-review" {
		t.Errorf("invocations = %+v, want both to be planning-readiness-review", invocations)
	}
}

// contains is a simple string contains check for test assertions.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestValidateProposedDecisions_ReusesPlanningSurvey(t *testing.T) {
	// This test ensures the validation reuses planningsurvey's rules
	err := planningsurvey.ValidateProposedDecisions([]planningsurvey.ProposedDecision{
		{TempKey: "", Title: "Test", Question: "Q?", Consequential: true},
	})
	if err == nil {
		t.Fatal("ValidateProposedDecisions: want error for blank temp_key, got nil")
	}

	err = planningsurvey.ValidateProposedDecisions([]planningsurvey.ProposedDecision{
		{TempKey: "a", Title: "Test", Question: "Q?", Consequential: true},
		{TempKey: "a", Title: "Test2", Question: "Q2?", Consequential: true},
	})
	if err == nil {
		t.Fatal("ValidateProposedDecisions: want error for duplicate temp_key, got nil")
	}

	err = planningsurvey.ValidateProposedDecisions([]planningsurvey.ProposedDecision{
		{TempKey: "a", Title: "", Question: "Q?", Consequential: true},
	})
	if err == nil {
		t.Fatal("ValidateProposedDecisions: want error for blank title, got nil")
	}
}

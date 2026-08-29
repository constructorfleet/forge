package decisionresolution_test

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/decisionresolution"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
)

func planningContext(t *testing.T, decisions ...planningagent.NamedArtifact) planningagent.PlanningContext {
	t.Helper()
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Sections: []planning.Section{{Heading: "Goal", Body: "Ship a widget."}},
	}
	artifacts := append([]planningagent.NamedArtifact{{ID: "goal", Artifact: goal}}, decisions...)
	pc, err := planningagent.Compile(agent.RepositoryContext{BaseRevision: "base"}, artifacts, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return pc
}

func questionDecision(question string) *planning.Artifact {
	return &planning.Artifact{
		Kind:     planning.KindDecision,
		Sections: []planning.Section{{Heading: "Question", Body: question}},
	}
}

func TestResolve_DecodesResult(t *testing.T) {
	pc := planningContext(t, planningagent.NamedArtifact{ID: "001-storage", Artifact: questionDecision("Where does state live?")})

	backend := planningagent.NewFakeBackend()
	backend.ProgramDefault("```json\n" +
		`{"outcome":"SQLite","rationale":"simplest for MVP","consequences":"single-writer",` +
		`"assumptions":"low concurrency","new_unknowns":[` +
		`{"temp_key":"a","title":"Pick migration tool","question":"?","depends_on":[],"consequential":true}` +
		`]}` +
		"\n```\n")

	res, err := decisionresolution.Resolve(context.Background(), backend, decisionresolution.Request{Context: pc, TargetID: "001-storage"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Outcome != "SQLite" {
		t.Errorf("Outcome = %q, want SQLite", res.Outcome)
	}
	if len(res.NewUnknowns) != 1 || res.NewUnknowns[0].TempKey != "a" {
		t.Errorf("NewUnknowns = %+v, want one entry with temp_key a", res.NewUnknowns)
	}

	invocations := backend.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("Invocations() len = %d, want 1", len(invocations))
	}
	if invocations[0].Prompt == "" {
		t.Errorf("prompt was empty")
	}
}

func TestResolve_RejectsUnknownTarget(t *testing.T) {
	pc := planningContext(t)
	backend := planningagent.NewFakeBackend()
	backend.ProgramDefault("```json\n" + `{"outcome":"x"}` + "\n```\n")

	if _, err := decisionresolution.Resolve(context.Background(), backend, decisionresolution.Request{Context: pc, TargetID: "missing"}); err == nil {
		t.Fatal("Resolve: want error for target not present in context, got nil")
	}
	if len(backend.Invocations()) != 0 {
		t.Error("Resolve invoked the backend despite an invalid target")
	}
}

func TestResolve_RejectsBlankOutcome(t *testing.T) {
	pc := planningContext(t, planningagent.NamedArtifact{ID: "001-storage", Artifact: questionDecision("?")})
	backend := planningagent.NewFakeBackend()
	backend.ProgramDefault("```json\n" + `{"outcome":""}` + "\n```\n")

	if _, err := decisionresolution.Resolve(context.Background(), backend, decisionresolution.Request{Context: pc, TargetID: "001-storage"}); err == nil {
		t.Fatal("Resolve: want error for blank outcome, got nil")
	}
}

func TestResolve_RejectsInvalidNewUnknown(t *testing.T) {
	pc := planningContext(t, planningagent.NamedArtifact{ID: "001-storage", Artifact: questionDecision("?")})
	backend := planningagent.NewFakeBackend()
	backend.ProgramDefault("```json\n" +
		`{"outcome":"SQLite","new_unknowns":[{"temp_key":"","title":"x","consequential":true}]}` +
		"\n```\n")

	if _, err := decisionresolution.Resolve(context.Background(), backend, decisionresolution.Request{Context: pc, TargetID: "001-storage"}); err == nil {
		t.Fatal("Resolve: want error for blank new_unknown temp_key, got nil")
	}
}

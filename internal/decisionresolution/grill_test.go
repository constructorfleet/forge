package decisionresolution_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/decisionresolution"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
)

// TestResolve_GrillLoadBearing_PromptDefersLoadBearing confirms the grill flag
// adds the "defer load-bearing Decisions" instruction to the prompt, so the
// resolver escalates a genuine choice to the human instead of guessing.
func TestResolve_GrillLoadBearing_PromptDefersLoadBearing(t *testing.T) {
	pc := planningContext(t, planningagent.NamedArtifact{ID: "001-storage", Artifact: questionDecision("Where does state live?")})

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("decision-resolution", `{"needs_human":{"question":"Which store?","context":"both work"}}`)

	res, err := decisionresolution.Resolve(context.Background(), backend, decisionresolution.Request{
		Context:          pc,
		TargetID:         "001-storage",
		GrillLoadBearing: true,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.NeedsHuman == nil {
		t.Fatalf("expected a needs_human result, got %+v", res)
	}

	prompt := backend.Invocations()[0].Prompt
	if !strings.Contains(prompt, "load-bearing") || !strings.Contains(prompt, "needs_human rather than choosing yourself") {
		t.Errorf("grill prompt missing the defer-load-bearing instruction:\n%s", prompt)
	}
}

// TestResolve_HumanGuidance_PromptResolvesFromAnswer confirms a Decision that
// carries a human answer (the resume path) is resolved from that answer and is
// never deferred, whatever the grill flag is.
func TestResolve_HumanGuidance_PromptResolvesFromAnswer(t *testing.T) {
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Sections: []planning.Section{{Heading: "Goal", Body: "Ship a widget."}},
	}
	artifacts := []planningagent.NamedArtifact{
		{ID: "goal", Artifact: goal},
		{ID: "001-vendor", Artifact: questionDecision("Which vendor?")},
	}
	humanInputs := map[string]string{"001-vendor": "Use vendor A -- we already have a contract."}
	pc, err := planningagent.Compile(agent.RepositoryContext{BaseRevision: "base"}, artifacts, humanInputs)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("decision-resolution", `{"outcome":"Vendor A","rationale":"human chose it","consequences":"none","assumptions":"none"}`)

	res, err := decisionresolution.Resolve(context.Background(), backend, decisionresolution.Request{
		Context:          pc,
		TargetID:         "001-vendor",
		GrillLoadBearing: true, // must be overridden by the human guidance
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Outcome != "Vendor A" {
		t.Errorf("Outcome = %q, want Vendor A", res.Outcome)
	}

	prompt := backend.Invocations()[0].Prompt
	if !strings.Contains(prompt, "human has already answered") {
		t.Errorf("prompt missing the human-guidance instruction:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Use vendor A") {
		t.Errorf("prompt missing the human's answer text:\n%s", prompt)
	}
	if strings.Contains(prompt, "Defer load-bearing Decisions") {
		t.Errorf("human-guidance prompt must not also carry the grill instruction:\n%s", prompt)
	}
}

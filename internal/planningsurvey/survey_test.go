package planningsurvey_test

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/planningsurvey"
)

func goalContext(t *testing.T) planningagent.PlanningContext {
	t.Helper()
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Sections: []planning.Section{{Heading: "Goal", Body: "Ship a widget."}},
	}
	pc, err := planningagent.Compile(agent.RepositoryContext{BaseRevision: "base"}, []planningagent.NamedArtifact{
		{ID: "goal", Artifact: goal},
	}, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return pc
}

func TestPropose_DecodesProposedDecisions(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramDefault(`` +
		"```json\n" +
		`{"decisions":[` +
		`{"temp_key":"a","title":"Pick storage","question":"Where does state live?","depends_on":[],"consequential":true},` +
		`{"temp_key":"b","title":"Pick logger","question":"Which logger?","depends_on":["a"],"consequential":false}` +
		`]}` +
		"\n```\n")

	res, err := planningsurvey.Propose(context.Background(), backend, goalContext(t))
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.Decisions) != 2 {
		t.Fatalf("len(Decisions) = %d, want 2", len(res.Decisions))
	}
	if res.Decisions[0].TempKey != "a" || !res.Decisions[0].Consequential {
		t.Errorf("Decisions[0] = %+v, want consequential temp_key a", res.Decisions[0])
	}
	if res.Decisions[1].Consequential {
		t.Errorf("Decisions[1].Consequential = true, want false")
	}

	invocations := backend.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("Invocations() len = %d, want 1", len(invocations))
	}
	if invocations[0].Prompt == "" {
		t.Errorf("prompt was empty")
	}
}

func TestPropose_RejectsBlankTempKey(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramDefault("```json\n" + `{"decisions":[{"temp_key":"","title":"x","depends_on":[],"consequential":true}]}` + "\n```\n")

	if _, err := planningsurvey.Propose(context.Background(), backend, goalContext(t)); err == nil {
		t.Fatal("Propose: want error for blank temp_key, got nil")
	}
}

func TestPropose_RejectsDuplicateTempKey(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramDefault("```json\n" +
		`{"decisions":[` +
		`{"temp_key":"a","title":"x","depends_on":[],"consequential":true},` +
		`{"temp_key":"a","title":"y","depends_on":[],"consequential":true}` +
		`]}` + "\n```\n")

	if _, err := planningsurvey.Propose(context.Background(), backend, goalContext(t)); err == nil {
		t.Fatal("Propose: want error for duplicate temp_key, got nil")
	}
}

func TestPropose_RejectsBlankTitle(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramDefault("```json\n" + `{"decisions":[{"temp_key":"a","title":"","depends_on":[],"consequential":true}]}` + "\n```\n")

	if _, err := planningsurvey.Propose(context.Background(), backend, goalContext(t)); err == nil {
		t.Fatal("Propose: want error for blank title, got nil")
	}
}

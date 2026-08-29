package wayfinding_test

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/wayfinding"
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

// fakePersist records every persisted (id, artifact) pair, mimicking a real
// caller writing each change to disk as Loop makes it.
type fakePersist struct {
	calls []string
}

func (f *fakePersist) persist(id string, a *planning.Artifact) error {
	f.calls = append(f.calls, id)
	return nil
}

func TestLoop_ResolvesReadyDecisionsInDependencyOrder(t *testing.T) {
	goal := goalArtifact()
	goalRef := decisiongraph.GoalRef{ID: "goal", Revision: goal.Revision}

	decisions := map[string]*planning.Artifact{
		"002-logger":  questionDecision("Which logger?", planning.DerivedFromEntry{Kind: planning.KindDecision, ID: "001-storage", Revision: "whatever"}),
		"001-storage": questionDecision("Where does state live?"),
	}

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("decision-resolution", "```json\n"+`{"outcome":"SQLite"}`+"\n```\n")
	backend.ProgramResult("decision-resolution", "```json\n"+`{"outcome":"stdlib log"}`+"\n```\n")

	persist := &fakePersist{}
	if err := wayfinding.Loop(context.Background(), backend, agent.RepositoryContext{BaseRevision: "base"}, goal, goalRef, decisions, persist.persist); err != nil {
		t.Fatalf("Loop: %v", err)
	}

	if !planning.Ready(decisions["001-storage"]) {
		t.Error("001-storage was not resolved")
	}
	if !planning.Ready(decisions["002-logger"]) {
		t.Error("002-logger was not resolved")
	}
	if len(decisiongraph.Frontier(decisions)) != 0 {
		t.Errorf("Frontier after Loop = %v, want empty", decisiongraph.Frontier(decisions))
	}

	if len(persist.calls) != 2 || persist.calls[0] != "001-storage" || persist.calls[1] != "002-logger" {
		t.Errorf("persist calls = %v, want [001-storage 002-logger] in that order", persist.calls)
	}

	invocations := backend.Invocations()
	if len(invocations) != 2 {
		t.Fatalf("Invocations() len = %d, want 2 (one per Decision, no shared conversation)", len(invocations))
	}
}

func TestLoop_SpawnsNewUnknownsAndRecomputesFrontier(t *testing.T) {
	goal := goalArtifact()
	goalRef := decisiongraph.GoalRef{ID: "goal", Revision: goal.Revision}

	decisions := map[string]*planning.Artifact{
		"001-storage": questionDecision("Where does state live?"),
	}

	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("decision-resolution", "```json\n"+
		`{"outcome":"SQLite","new_unknowns":[`+
		`{"temp_key":"a","title":"Pick migration tool","question":"Which tool?","depends_on":[],"consequential":true},`+
		`{"temp_key":"skip","title":"incidental","consequential":false}`+
		`]}`+
		"\n```\n")
	backend.ProgramResult("decision-resolution", "```json\n"+`{"outcome":"golang-migrate"}`+"\n```\n")

	persist := &fakePersist{}
	if err := wayfinding.Loop(context.Background(), backend, agent.RepositoryContext{BaseRevision: "base"}, goal, goalRef, decisions, persist.persist); err != nil {
		t.Fatalf("Loop: %v", err)
	}

	if len(decisions) != 2 {
		t.Fatalf("len(decisions) = %d, want 2 (storage + spawned migration-tool)", len(decisions))
	}

	var spawnedID string
	for id := range decisions {
		if id != "001-storage" {
			spawnedID = id
		}
	}
	if spawnedID == "" || !planning.Ready(decisions[spawnedID]) {
		t.Fatalf("spawned decision %q was not resolved", spawnedID)
	}

	if len(persist.calls) != 3 {
		t.Errorf("persist calls = %v, want 3 (storage resolved, spawned materialized, spawned resolved)", persist.calls)
	}
}

func TestLoop_NoFrontierIsANoop(t *testing.T) {
	goal := goalArtifact()
	goalRef := decisiongraph.GoalRef{ID: "goal", Revision: goal.Revision}

	d := questionDecision("Where does state live?")
	d.ApprovedRevision = d.Revision
	decisions := map[string]*planning.Artifact{"001-storage": d}

	backend := planningagent.NewFakeBackend()
	backend.ProgramDefault("```json\n" + `{"outcome":"should not be invoked"}` + "\n```\n")

	persist := &fakePersist{}
	if err := wayfinding.Loop(context.Background(), backend, agent.RepositoryContext{BaseRevision: "base"}, goal, goalRef, decisions, persist.persist); err != nil {
		t.Fatalf("Loop: %v", err)
	}

	if len(backend.Invocations()) != 0 {
		t.Error("Loop invoked the backend despite an already-resolved Decision and empty frontier")
	}
	if len(persist.calls) != 0 {
		t.Error("Loop persisted despite doing no work")
	}
}

func TestLoop_ResumesFromPartiallyResolvedState(t *testing.T) {
	goal := goalArtifact()
	goalRef := decisiongraph.GoalRef{ID: "goal", Revision: goal.Revision}

	resolvedStorage := questionDecision("Where does state live?")
	resolvedStorage.ApprovedRevision = resolvedStorage.Revision

	decisions := map[string]*planning.Artifact{
		"001-storage": resolvedStorage,
		"002-logger":  questionDecision("Which logger?", planning.DerivedFromEntry{Kind: planning.KindDecision, ID: "001-storage", Revision: "whatever"}),
	}

	backend := planningagent.NewFakeBackend()
	backend.ProgramDefault("```json\n" + `{"outcome":"stdlib log"}` + "\n```\n")

	persist := &fakePersist{}
	if err := wayfinding.Loop(context.Background(), backend, agent.RepositoryContext{BaseRevision: "base"}, goal, goalRef, decisions, persist.persist); err != nil {
		t.Fatalf("Loop: %v", err)
	}

	if len(backend.Invocations()) != 1 {
		t.Fatalf("Invocations() len = %d, want 1 (001-storage already resolved, must not be re-resolved)", len(backend.Invocations()))
	}
	if !planning.Ready(decisions["002-logger"]) {
		t.Error("002-logger was not resolved on resume")
	}
}

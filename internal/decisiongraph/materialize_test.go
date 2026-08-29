package decisiongraph_test

import (
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningsurvey"
)

func TestMaterialize_AssignsRealIdentityInDependencyOrder(t *testing.T) {
	proposed := []planningsurvey.ProposedDecision{
		{TempKey: "b", Title: "Pick logger", Question: "Which logger?", DependsOn: []string{"a"}, Consequential: true},
		{TempKey: "a", Title: "Pick Storage!", Question: "Where does state live?", Consequential: true},
	}

	out, err := decisiongraph.Materialize(proposed, decisiongraph.GoalRef{ID: "goal", Revision: "goalrev"}, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}

	// "a" has no dependencies, so it must be assigned before "b" despite b
	// appearing first in proposed.
	if out[0].TempKey != "a" || out[1].TempKey != "b" {
		t.Fatalf("materialization order = [%s %s], want [a b]", out[0].TempKey, out[1].TempKey)
	}
	if out[0].ID != "001-pick-storage" {
		t.Errorf("out[0].ID = %q, want 001-pick-storage", out[0].ID)
	}
	if out[1].ID != "002-pick-logger" {
		t.Errorf("out[1].ID = %q, want 002-pick-logger", out[1].ID)
	}

	// b's dependency edge must resolve to a's real ID and computed revision.
	found := false
	for _, dep := range out[1].Artifact.DerivedFrom {
		if dep.Kind == planning.KindDecision {
			found = true
			if dep.ID != out[0].ID {
				t.Errorf("b depends_on resolved to %q, want %q", dep.ID, out[0].ID)
			}
			if dep.Revision != out[0].Artifact.Revision {
				t.Errorf("b dependency revision = %q, want %q", dep.Revision, out[0].Artifact.Revision)
			}
		}
	}
	if !found {
		t.Error("b's Artifact has no decision-kind DerivedFrom entry")
	}

	for _, m := range out {
		hasGoal := false
		for _, dep := range m.Artifact.DerivedFrom {
			if dep.Kind == planning.KindGoal && dep.ID == "goal" && dep.Revision == "goalrev" {
				hasGoal = true
			}
		}
		if !hasGoal {
			t.Errorf("%s: missing goal DerivedFrom entry", m.ID)
		}
		if m.Artifact.Revision == "" {
			t.Errorf("%s: Revision not set", m.ID)
		}
		if m.Artifact.Revision != planning.ComputeRevision(m.Artifact) {
			t.Errorf("%s: Revision is stale relative to its own content", m.ID)
		}
	}
}

func TestMaterialize_DropsNonConsequentialProposals(t *testing.T) {
	proposed := []planningsurvey.ProposedDecision{
		{TempKey: "a", Title: "Pick storage", Question: "?", Consequential: true, DependsOn: []string{"skip"}},
		{TempKey: "skip", Title: "Pick a logger later", Question: "?", Consequential: false},
	}

	out, err := decisiongraph.Materialize(proposed, decisiongraph.GoalRef{ID: "goal"}, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1 (non-consequential proposal must be dropped)", len(out))
	}
	if out[0].TempKey != "a" {
		t.Fatalf("out[0].TempKey = %q, want a", out[0].TempKey)
	}
	for _, dep := range out[0].Artifact.DerivedFrom {
		if dep.Kind == planning.KindDecision {
			t.Errorf("dropped decision left a dangling dependency edge: %+v", dep)
		}
	}
}

func TestMaterialize_DetectsCycles(t *testing.T) {
	proposed := []planningsurvey.ProposedDecision{
		{TempKey: "a", Title: "A", Consequential: true, DependsOn: []string{"b"}},
		{TempKey: "b", Title: "B", Consequential: true, DependsOn: []string{"a"}},
	}

	_, err := decisiongraph.Materialize(proposed, decisiongraph.GoalRef{ID: "goal"}, nil)
	if err == nil {
		t.Fatal("Materialize: want cycle error, got nil")
	}
	var cycleErr *decisiongraph.CycleError
	if !errors.As(err, &cycleErr) {
		t.Fatalf("err = %v, want *decisiongraph.CycleError", err)
	}
}

func TestMaterialize_ContinuesNumberingAndAvoidsSlugCollisions(t *testing.T) {
	proposed := []planningsurvey.ProposedDecision{
		{TempKey: "a", Title: "Pick storage", Consequential: true},
	}

	out, err := decisiongraph.Materialize(proposed, decisiongraph.GoalRef{ID: "goal"}, []string{"001-pick-storage", "003-something-else"})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if out[0].ID != "004-pick-storage-2" {
		t.Errorf("out[0].ID = %q, want 004-pick-storage-2 (numbering continues past 003, slug disambiguated)", out[0].ID)
	}
}

func TestMaterialize_DuplicateTempKeyErrors(t *testing.T) {
	proposed := []planningsurvey.ProposedDecision{
		{TempKey: "a", Title: "A", Consequential: true},
		{TempKey: "a", Title: "A again", Consequential: true},
	}
	if _, err := decisiongraph.Materialize(proposed, decisiongraph.GoalRef{ID: "goal"}, nil); err == nil {
		t.Fatal("Materialize: want error for duplicate temp key, got nil")
	}
}

package decisiongraph_test

import (
	"reflect"
	"testing"

	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/planning"
)

func decision(derivedFrom ...planning.DerivedFromEntry) *planning.Artifact {
	return &planning.Artifact{Kind: planning.KindDecision, DerivedFrom: derivedFrom}
}

func approve(a *planning.Artifact) *planning.Artifact {
	a.Revision = planning.ComputeRevision(a)
	a.ApprovedRevision = a.Revision
	return a
}

func TestFrontier_NoDependencies_IsOnFrontier(t *testing.T) {
	decisions := map[string]*planning.Artifact{
		"001-a": decision(),
	}
	if got := decisiongraph.Frontier(decisions); !reflect.DeepEqual(got, []string{"001-a"}) {
		t.Errorf("Frontier = %v, want [001-a]", got)
	}
}

func TestFrontier_ApprovedDecisionIsNotOnFrontier(t *testing.T) {
	decisions := map[string]*planning.Artifact{
		"001-a": approve(decision()),
	}
	if got := decisiongraph.Frontier(decisions); len(got) != 0 {
		t.Errorf("Frontier = %v, want empty (already approved)", got)
	}
}

func TestFrontier_BlockedUntilDependencyApproved(t *testing.T) {
	dep := decision()
	dependent := decision(planning.DerivedFromEntry{Kind: planning.KindDecision, ID: "001-a", Revision: "whatever"})
	decisions := map[string]*planning.Artifact{
		"001-a": dep,
		"002-b": dependent,
	}

	got := decisiongraph.Frontier(decisions)
	if !reflect.DeepEqual(got, []string{"001-a"}) {
		t.Fatalf("Frontier = %v, want [001-a] (002-b still blocked)", got)
	}

	approve(dep)
	got = decisiongraph.Frontier(decisions)
	if !reflect.DeepEqual(got, []string{"002-b"}) {
		t.Errorf("Frontier = %v, want [002-b] once 001-a is approved", got)
	}
}

func TestFrontier_GoalDerivedFromIsIgnoredForBlocking(t *testing.T) {
	decisions := map[string]*planning.Artifact{
		"001-a": decision(planning.DerivedFromEntry{Kind: planning.KindGoal, ID: "goal", Revision: "rev"}),
	}
	if got := decisiongraph.Frontier(decisions); !reflect.DeepEqual(got, []string{"001-a"}) {
		t.Errorf("Frontier = %v, want [001-a] (goal DerivedFrom does not block)", got)
	}
}

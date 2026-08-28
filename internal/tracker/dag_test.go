package tracker_test

import (
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

func dep(issueID, dependsOnID string) domain.Dependency {
	return domain.Dependency{IssueID: issueID, DependsOnID: dependsOnID}
}

func TestBuildDAG_LinearChain(t *testing.T) {
	issues := []domain.Issue{
		{ID: "1", Dependencies: nil},
		{ID: "2", Dependencies: []domain.Dependency{dep("2", "1")}},
		{ID: "3", Dependencies: []domain.Dependency{dep("3", "2")}},
	}

	d, err := tracker.BuildDAG(issues)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.HasNode("1") || !d.HasNode("2") || !d.HasNode("3") {
		t.Fatalf("expected all issue nodes present")
	}
}

func TestBuildDAG_ObservesExternalDependencyNode(t *testing.T) {
	// Issue 2 depends on issue 99, which is not itself in the issue list
	// (an External Issue, see CONTEXT.md). It must still be a DAG node.
	issues := []domain.Issue{
		{ID: "2", Dependencies: []domain.Dependency{dep("2", "99")}},
	}

	d, err := tracker.BuildDAG(issues)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.HasNode("99") {
		t.Fatal("expected external dependency node 99 to be observed in the DAG")
	}
}

func TestBuildDAG_DetectsDirectCycle(t *testing.T) {
	issues := []domain.Issue{
		{ID: "1", Dependencies: []domain.Dependency{dep("1", "2")}},
		{ID: "2", Dependencies: []domain.Dependency{dep("2", "1")}},
	}

	_, err := tracker.BuildDAG(issues)
	if err == nil {
		t.Fatal("expected a cycle error, got nil")
	}
	var cycleErr *tracker.CycleError
	if !errors.As(err, &cycleErr) {
		t.Fatalf("expected *tracker.CycleError, got %T: %v", err, err)
	}
}

func TestBuildDAG_DetectsIndirectCycle(t *testing.T) {
	issues := []domain.Issue{
		{ID: "1", Dependencies: []domain.Dependency{dep("1", "2")}},
		{ID: "2", Dependencies: []domain.Dependency{dep("2", "3")}},
		{ID: "3", Dependencies: []domain.Dependency{dep("3", "1")}},
	}

	_, err := tracker.BuildDAG(issues)
	if err == nil {
		t.Fatal("expected a cycle error, got nil")
	}
	var cycleErr *tracker.CycleError
	if !errors.As(err, &cycleErr) {
		t.Fatalf("expected *tracker.CycleError, got %T: %v", err, err)
	}
}

func TestBuildDAG_SelfDependencyIsACycle(t *testing.T) {
	issues := []domain.Issue{
		{ID: "1", Dependencies: []domain.Dependency{dep("1", "1")}},
	}

	_, err := tracker.BuildDAG(issues)
	if err == nil {
		t.Fatal("expected a cycle error for self-dependency, got nil")
	}
}

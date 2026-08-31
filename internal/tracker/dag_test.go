package tracker_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

// fakeDependencyStore is a DependencyStore backed by an in-memory edge map,
// keyed by Issue.ID — the seam BuildDAGFromStore reads through instead of
// domain.Issue.Dependencies.
type fakeDependencyStore struct {
	edges map[string][]tracker.DependencyEdge
	err   error
}

func (f *fakeDependencyStore) GetDependencies(_ context.Context, id string) ([]tracker.DependencyEdge, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.edges[id], nil
}

// WriteDependencies is unused by these tests — BuildDAGFromStore only reads
// — but fakeDependencyStore must implement the full interface to stand in
// for tracker.DependencyStore.
func (f *fakeDependencyStore) WriteDependencies(context.Context, string, []string) error {
	return nil
}

func blocksEdge(issueID, dependsOnID string) tracker.DependencyEdge {
	return tracker.DependencyEdge{
		Issue:     domain.IssueRef{Provider: "github", ID: issueID},
		DependsOn: domain.IssueRef{Provider: "github", ID: dependsOnID},
		Kind:      tracker.DependencyBlocks,
	}
}

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

func TestBuildDAGFromStore_ReadsEdgesThroughDependencyStore(t *testing.T) {
	store := &fakeDependencyStore{edges: map[string][]tracker.DependencyEdge{
		"2": {blocksEdge("2", "1")},
		"3": {blocksEdge("3", "2")},
	}}

	d, err := tracker.BuildDAGFromStore(context.Background(), store, []string{"1", "2", "3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.HasNode("1") || !d.HasNode("2") || !d.HasNode("3") {
		t.Fatalf("expected all issue nodes present")
	}
	if got := d.DependsOn("2"); len(got) != 1 || got[0] != "1" {
		t.Fatalf("DependsOn(2) = %v, want [1]", got)
	}
	if got := d.DependsOn("3"); len(got) != 1 || got[0] != "2" {
		t.Fatalf("DependsOn(3) = %v, want [2]", got)
	}
}

func TestBuildDAGFromStore_ObservesExternalDependencyNode(t *testing.T) {
	store := &fakeDependencyStore{edges: map[string][]tracker.DependencyEdge{
		"2": {blocksEdge("2", "99")},
	}}

	d, err := tracker.BuildDAGFromStore(context.Background(), store, []string{"2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.HasNode("99") {
		t.Fatalf("expected External Issue 99 to be observed as a node")
	}
}

func TestBuildDAGFromStore_DetectsCycle(t *testing.T) {
	store := &fakeDependencyStore{edges: map[string][]tracker.DependencyEdge{
		"1": {blocksEdge("1", "2")},
		"2": {blocksEdge("2", "1")},
	}}

	_, err := tracker.BuildDAGFromStore(context.Background(), store, []string{"1", "2"})
	if err == nil {
		t.Fatal("expected a cycle error, got nil")
	}
	var cycleErr *tracker.CycleError
	if !errors.As(err, &cycleErr) {
		t.Fatalf("expected *tracker.CycleError, got %T: %v", err, err)
	}
}

func TestBuildDAGFromStore_PropagatesStoreError(t *testing.T) {
	wantErr := fmt.Errorf("boom")
	store := &fakeDependencyStore{err: wantErr}

	_, err := tracker.BuildDAGFromStore(context.Background(), store, []string{"1"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped store error, got %v", err)
	}
}

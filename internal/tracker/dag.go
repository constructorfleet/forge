package tracker

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Teagan42/forge/internal/domain"
)

// DAG is the dependency graph built from a set of Issues' Dependencies (see
// CONTEXT.md "Dependency"). It observes every node reachable from a
// Dependency edge, including External Issues that are not themselves part
// of the Execution set (see CONTEXT.md "External Issue").
type DAG struct {
	nodes map[string]bool
	edges map[string][]string // issueID -> depends-on IDs
}

// HasNode reports whether id is a node in the DAG, either because it is one
// of the Issues the DAG was built from or because another Issue depends on
// it (an observed External Issue).
func (d *DAG) HasNode(id string) bool { return d.nodes[id] }

// DependsOn returns the IDs the given Issue directly depends on.
func (d *DAG) DependsOn(id string) []string { return d.edges[id] }

// CycleError is returned by BuildDAG when the Dependency graph contains a
// cycle. Cycles must be detected before any Worker launches (see
// CONTEXT.md "Dependency").
type CycleError struct {
	// Cycle lists the Issue IDs forming the cycle, in traversal order, with
	// the first ID repeated at the end to make the loop explicit.
	Cycle []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("tracker: dependency cycle detected: %s", strings.Join(e.Cycle, " -> "))
}

// buildDAG is the shared graph-construction core for BuildDAG and
// BuildDAGFromStore: allocate an empty DAG, add ids as nodes plus whatever
// dependsOn edges next reports for each, then run cycle detection before
// returning. The two public constructors differ only in how next resolves
// an Issue ID's DependsOn IDs (a pre-populated field vs. a DependencyStore
// read), so that is the only thing they pass in.
func buildDAG(ids []string, next func(id string) ([]string, error)) (*DAG, error) {
	d := &DAG{
		nodes: make(map[string]bool),
		edges: make(map[string][]string),
	}

	for _, id := range ids {
		d.nodes[id] = true
		dependsOn, err := next(id)
		if err != nil {
			return nil, err
		}
		for _, dependsOnID := range dependsOn {
			d.nodes[dependsOnID] = true
			d.edges[id] = append(d.edges[id], dependsOnID)
		}
	}

	if cycle := d.findCycle(); cycle != nil {
		return nil, &CycleError{Cycle: cycle}
	}

	return d, nil
}

// BuildDAG constructs the Dependency DAG from a set of Issues and detects
// cycles before returning. A non-nil error is always a *CycleError.
func BuildDAG(issues []domain.Issue) (*DAG, error) {
	ids := make([]string, len(issues))
	dependsOnByID := make(map[string][]string, len(issues))
	for i, issue := range issues {
		ids[i] = issue.ID
		dependsOn := make([]string, len(issue.Dependencies))
		for j, dp := range issue.Dependencies {
			dependsOn[j] = dp.DependsOnID
		}
		dependsOnByID[issue.ID] = dependsOn
	}

	return buildDAG(ids, func(id string) ([]string, error) {
		return dependsOnByID[id], nil
	})
}

// BuildDAGFromStore constructs the Dependency DAG the same way BuildDAG
// does, but reads each Issue's DependencyEdges through the DependencyStore
// capability (GetDependencies) instead of a pre-populated
// domain.Issue.Dependencies field — this is the DependencyStore read path
// callers (the scheduler, the single-Issue engine) build the DAG from. As
// with BuildDAG, External Issues named only as a DependsOn (see CONTEXT.md
// "External Issue") become observed nodes even though ids never names them.
func BuildDAGFromStore(ctx context.Context, store DependencyStore, ids []string) (*DAG, error) {
	return buildDAG(ids, func(id string) ([]string, error) {
		edges, err := store.GetDependencies(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("tracker: fetch dependencies for issue %s: %w", id, err)
		}
		dependsOn := make([]string, len(edges))
		for i, edge := range edges {
			dependsOn[i] = edge.DependsOn.ID
		}
		return dependsOn, nil
	})
}

// findCycle runs DFS from every node, tracking the current path so a cycle
// can be reported as the exact loop of Issue IDs involved.
func (d *DAG) findCycle() []string {
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make(map[string]int, len(d.nodes))

	var path []string
	var visit func(id string) []string
	visit = func(id string) []string {
		state[id] = visiting
		path = append(path, id)

		for _, next := range d.edges[id] {
			switch state[next] {
			case visiting:
				// Found the cycle: the path from next's first occurrence to
				// here, plus next again to close the loop.
				for i, p := range path {
					if p == next {
						cycle := append([]string{}, path[i:]...)
						return append(cycle, next)
					}
				}
			case unvisited:
				if cyc := visit(next); cyc != nil {
					return cyc
				}
			}
		}

		path = path[:len(path)-1]
		state[id] = done
		return nil
	}

	// Deterministic iteration order for reproducible error messages.
	ids := make([]string, 0, len(d.nodes))
	for id := range d.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		if state[id] == unvisited {
			if cyc := visit(id); cyc != nil {
				return cyc
			}
		}
	}
	return nil
}

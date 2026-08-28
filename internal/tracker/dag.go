package tracker

import (
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

// BuildDAG constructs the Dependency DAG from a set of Issues and detects
// cycles before returning. A non-nil error is always a *CycleError.
func BuildDAG(issues []domain.Issue) (*DAG, error) {
	d := &DAG{
		nodes: make(map[string]bool),
		edges: make(map[string][]string),
	}

	for _, issue := range issues {
		d.nodes[issue.ID] = true
		for _, dp := range issue.Dependencies {
			d.nodes[dp.DependsOnID] = true
			d.edges[issue.ID] = append(d.edges[issue.ID], dp.DependsOnID)
		}
	}

	if cycle := d.findCycle(); cycle != nil {
		return nil, &CycleError{Cycle: cycle}
	}

	return d, nil
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

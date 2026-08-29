// Package decisiongraph implements Forge's side of the PlanningSurvey
// pipeline (ticket 13): validating a PlanningSurvey's proposed Decisions,
// assigning them real identity in place of the agent's temporary keys, and
// computing the deterministic frontier of Decisions ready to be worked. All
// file and tracker mechanics live here, never in internal/planningsurvey --
// see that package's doc comment.
package decisiongraph

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningsurvey"
)

// GoalRef identifies the goal Artifact a batch of materialized Decisions
// records as derived from.
type GoalRef struct {
	ID       string
	Revision string
}

// MaterializedDecision pairs a Decision Artifact ready to render and write
// with the real ID Forge assigned it (the decisions/NNN-<slug>.md stem,
// without extension) and the ProposedDecision.TempKey it was resolved from.
type MaterializedDecision struct {
	ID       string
	TempKey  string
	Artifact *planning.Artifact
}

// CycleError is returned by Materialize when proposed's depends_on edges
// (restricted to consequential proposals) contain a cycle.
type CycleError struct {
	// Cycle lists the TempKeys forming the cycle, in traversal order, with
	// the first TempKey repeated at the end to make the loop explicit.
	Cycle []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("decisiongraph: dependency cycle detected: %s", strings.Join(e.Cycle, " -> "))
}

var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// Materialize validates proposed's dependency graph and returns the
// Decision Artifacts to persist, in an order safe to write: a decision
// always appears after every consequential decision it depends on, so its
// DerivedFrom entries can record dependency revisions that already exist.
//
// Non-consequential proposals (ProposedDecision.Consequential == false) are
// dropped entirely -- neither materialized nor left as dangling
// dependencies: any consequential proposal that names one as a dependency
// simply has that edge dropped, since a non-consequential unknown was never
// going to become a real blocking Decision.
//
// existingIDs is every Decision ID already materialized for the Feature
// (e.g. "007-auth-strategy"), used to keep the NNN sequence and slugs
// unique across repeated survey passes; it may be nil on a Feature's first
// survey.
//
// Materialize performs no I/O: callers render (planning.Render) and write
// each MaterializedDecision.Artifact themselves.
func Materialize(proposed []planningsurvey.ProposedDecision, goal GoalRef, existingIDs []string) ([]MaterializedDecision, error) {
	consequential := make(map[string]planningsurvey.ProposedDecision)
	order := make([]string, 0, len(proposed))
	seen := make(map[string]bool, len(proposed))
	for _, d := range proposed {
		if d.TempKey == "" {
			return nil, fmt.Errorf("decisiongraph: proposed decision has a blank temp key")
		}
		if seen[d.TempKey] {
			return nil, fmt.Errorf("decisiongraph: duplicate temp key %q", d.TempKey)
		}
		seen[d.TempKey] = true
		if !d.Consequential {
			continue
		}
		consequential[d.TempKey] = d
		order = append(order, d.TempKey)
	}

	edges := make(map[string][]string, len(consequential))
	for key, d := range consequential {
		for _, dep := range d.DependsOn {
			if _, ok := consequential[dep]; ok {
				edges[key] = append(edges[key], dep)
			}
			// A dependency on a non-consequential (or otherwise unknown)
			// temp key is dropped: it was never going to be a real
			// blocking Decision.
		}
	}

	topo, err := topoSort(order, edges)
	if err != nil {
		return nil, err
	}

	existingNumbers := make(map[int]bool)
	existingSlugs := make(map[string]bool)
	nextNumber := 1
	for _, id := range existingIDs {
		n, slug, ok := splitID(id)
		if !ok {
			continue
		}
		existingNumbers[n] = true
		existingSlugs[slug] = true
		if n >= nextNumber {
			nextNumber = n + 1
		}
	}

	realID := make(map[string]string, len(topo))
	revisionOf := make(map[string]string, len(topo))
	out := make([]MaterializedDecision, 0, len(topo))

	for _, key := range topo {
		d := consequential[key]

		slug := uniqueSlug(d.Title, existingSlugs)
		existingSlugs[slug] = true

		for existingNumbers[nextNumber] {
			nextNumber++
		}
		number := nextNumber
		existingNumbers[number] = true
		nextNumber++

		id := fmt.Sprintf("%03d-%s", number, slug)

		derivedFrom := []planning.DerivedFromEntry{
			{Kind: planning.KindGoal, ID: goal.ID, Revision: goal.Revision},
		}
		for _, dep := range edges[key] {
			derivedFrom = append(derivedFrom, planning.DerivedFromEntry{
				Kind:     planning.KindDecision,
				ID:       realID[dep],
				Revision: revisionOf[dep],
			})
		}

		artifact := &planning.Artifact{
			Kind:        planning.KindDecision,
			State:       "proposed",
			DerivedFrom: derivedFrom,
			Sections: []planning.Section{
				{Heading: "Question", Body: d.Question},
			},
		}
		artifact.Revision = planning.ComputeRevision(artifact)

		realID[key] = id
		revisionOf[key] = artifact.Revision
		out = append(out, MaterializedDecision{ID: id, TempKey: key, Artifact: artifact})
	}

	return out, nil
}

// topoSort returns keys in dependency order (a key's dependencies appear
// before it), deterministically breaking ties by TempKey so repeated runs
// over the same proposal produce the same NNN assignment.
func topoSort(keys []string, edges map[string][]string) ([]string, error) {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)

	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make(map[string]int, len(sorted))
	var path []string
	var out []string

	var visit func(key string) []string
	visit = func(key string) []string {
		state[key] = visiting
		path = append(path, key)

		deps := append([]string(nil), edges[key]...)
		sort.Strings(deps)
		for _, dep := range deps {
			switch state[dep] {
			case visiting:
				for i, p := range path {
					if p == dep {
						cycle := append([]string{}, path[i:]...)
						return append(cycle, dep)
					}
				}
			case unvisited:
				if cyc := visit(dep); cyc != nil {
					return cyc
				}
			}
		}

		path = path[:len(path)-1]
		state[key] = done
		out = append(out, key)
		return nil
	}

	for _, key := range sorted {
		if state[key] == unvisited {
			if cyc := visit(key); cyc != nil {
				return nil, &CycleError{Cycle: cyc}
			}
		}
	}
	return out, nil
}

// uniqueSlug derives a URL/filename-safe slug from title, disambiguating
// against taken by appending -2, -3, ... as needed.
func uniqueSlug(title string, taken map[string]bool) string {
	base := strings.Trim(slugNonAlnum.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if base == "" {
		base = "decision"
	}
	slug := base
	for n := 2; taken[slug]; n++ {
		slug = fmt.Sprintf("%s-%d", base, n)
	}
	return slug
}

var idPattern = regexp.MustCompile(`^(\d+)-(.+)$`)

// splitID parses a Decision ID of the form "NNN-slug" into its number and
// slug. It returns ok == false for anything else, so malformed IDs (never
// produced by Materialize itself, but possibly hand-authored) are ignored
// rather than corrupting numbering.
func splitID(id string) (number int, slug string, ok bool) {
	m := idPattern.FindStringSubmatch(id)
	if m == nil {
		return 0, "", false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, "", false
	}
	return n, m[2], true
}

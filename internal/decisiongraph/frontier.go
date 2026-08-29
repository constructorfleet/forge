package decisiongraph

import (
	"sort"

	"github.com/Teagan42/forge/internal/planning"
)

// Frontier returns the IDs of Decisions in decisions that are actionable
// now: not yet Ready (planning.Ready -- approved) themselves, but every
// Decision they depend on (their decision-kind DerivedFrom entries) is
// Ready. A Decision with no decision-kind dependencies is on the frontier
// as soon as it exists and isn't already Ready.
//
// Frontier is computed purely from Decision states and dependencies -- the
// same durable-content-only discipline internal/planning's predicates use
// -- so it is always derived, never stored. Result order is deterministic
// (sorted by ID).
func Frontier(decisions map[string]*planning.Artifact) []string {
	var frontier []string
	for id, d := range decisions {
		if planning.Ready(d) {
			continue
		}

		blocked := false
		for _, dep := range d.DerivedFrom {
			if dep.Kind != planning.KindDecision {
				continue
			}
			depArtifact, ok := decisions[dep.ID]
			if !ok || !planning.Ready(depArtifact) {
				blocked = true
				break
			}
		}
		if !blocked {
			frontier = append(frontier, id)
		}
	}

	sort.Strings(frontier)
	return frontier
}

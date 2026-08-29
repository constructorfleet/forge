// Package planningreadiness implements the PlanningReadinessReview planning
// contract (ticket 15): the fresh, independent check wayfinding runs once
// its Decision frontier empties, to decide whether the Feature's Decisions
// are actually sufficient to write a spec from, or whether the frontier
// only looked empty because more Decisions still need to be surfaced. It is
// a typed call site built on internal/planningagent's structured-invocation
// core, mirroring internal/planningsurvey's shape -- deliberately a fresh
// invocation with no memory of PlanningSurvey or any DecisionResolution
// that came before it, so it judges the current Decision set on its own
// merits rather than rubber-stamping the process that produced it.
//
// PlanningReadinessReview performs no file or tracker mechanics: it only
// proposes a verdict and, if not ready, further Decisions. See
// internal/wayfinding.Loop for how a NOT_READY verdict's Decisions are
// materialized and fed back onto the frontier.
package planningreadiness

import (
	"context"
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/planningsurvey"
)

// invocationKey identifies the PlanningReadinessReview contract to a
// scripted planningagent.Backend (see planningagent.FakeBackend.ProgramResult).
const invocationKey = "planning-readiness-review"

// Status values PlanningReadinessReview's Result.Status may hold.
const (
	// StatusReadyForSpec means wayfinding is done: every consequential
	// unknown has been surfaced and resolved, and the Decision set is
	// sufficient to write a spec from.
	StatusReadyForSpec = "READY_FOR_SPEC"

	// StatusNotReady means wayfinding must continue: Decisions lists
	// further consequential unknowns the reviewer surfaced that must be
	// resolved before a spec can be written.
	StatusNotReady = "NOT_READY"
)

// Result is PlanningReadinessReview's structured response.
type Result struct {
	Status    string                            `json:"status"`
	Decisions []planningsurvey.ProposedDecision `json:"decisions"`
}

// Review runs the PlanningReadinessReview contract against backend for pc
// (expected to carry the Feature's goal and every currently-resolved
// Decision), returning the raw verdict. Review performs no
// dependency-graph validation and writes nothing to disk -- see
// internal/decisiongraph.Materialize for turning a NOT_READY verdict's
// Decisions into Artifacts to persist.
func Review(ctx context.Context, backend planningagent.Backend, pc planningagent.PlanningContext) (Result, error) {
	return planningagent.InvokeStructured(ctx, backend, invocationKey, pc, buildPrompt, validateResult)
}

// buildPrompt renders pc's goal and every currently-resolved Decision into
// the PlanningReadinessReview prompt.
func buildPrompt(pc planningagent.PlanningContext) string {
	var b strings.Builder
	b.WriteString("You are Forge's PlanningReadinessReview. Read the Feature goal and every " +
		"resolved Decision below, then judge whether they are sufficient to write a spec " +
		"from. Do not assume the process that produced these Decisions was thorough -- judge " +
		"the current set on its own merits.\n\n" +
		"If the Decisions are sufficient, respond with status READY_FOR_SPEC and no decisions. " +
		"If a genuinely consequential unknown remains unaddressed -- one that would change the " +
		"spec or the implementation depending on how it's answered -- respond with status " +
		"NOT_READY and propose it as a decision, exactly like PlanningSurvey proposes new " +
		"Decisions.\n\n")

	if pc.Goal != nil {
		b.WriteString("## Goal\n\n")
		for _, heading := range []string{"Goal", "Summary", "Context"} {
			if body, ok := pc.Goal.Sections[heading]; ok && body != "" {
				fmt.Fprintf(&b, "### %s\n\n%s\n\n", heading, body)
			}
		}
	}

	if len(pc.Decisions) > 0 {
		b.WriteString("## Resolved decisions\n\n")
		for _, d := range pc.Decisions {
			fmt.Fprintf(&b, "- %s: %s\n", d.ID, d.Sections["Outcome"])
		}
		b.WriteString("\n")
	}

	b.WriteString("Respond with exactly one fenced json block containing:\n" +
		`{"status":"READY_FOR_SPEC|NOT_READY","decisions":[{"temp_key":"...","title":"...",` +
		`"question":"...","depends_on":["..."],"consequential":true}]}` + "\n")

	return b.String()
}

// validateResult rejects a structured response InvokeStructured cannot
// safely hand to Forge: a Status other than StatusReadyForSpec/StatusNotReady,
// a StatusReadyForSpec response that also proposes Decisions (there is
// nothing left to materialize if wayfinding is done), or a Decisions entry
// InvokeStructured cannot safely hand to Forge for materialization (see
// planningsurvey.ValidateProposedDecisions).
func validateResult(res Result) error {
	switch res.Status {
	case StatusReadyForSpec:
		if len(res.Decisions) > 0 {
			return fmt.Errorf("planningreadiness: status %s must not propose decisions", StatusReadyForSpec)
		}
	case StatusNotReady:
		if len(res.Decisions) == 0 {
			return fmt.Errorf("planningreadiness: status %s must propose at least one decision", StatusNotReady)
		}
	default:
		return fmt.Errorf("planningreadiness: unrecognized status %q", res.Status)
	}
	return planningsurvey.ValidateProposedDecisions(res.Decisions)
}

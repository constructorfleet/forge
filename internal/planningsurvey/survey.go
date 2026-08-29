// Package planningsurvey implements the PlanningSurvey planning contract
// (ticket 13): the first wayfinding pass, which turns a Feature goal into a
// proposed set of Decisions with typed dependencies between them. It is a
// typed call site built on internal/planningagent's structured-invocation
// core (see that package's doc comment).
//
// PlanningSurvey performs no file or tracker mechanics: it only proposes
// Decisions, keyed by caller-scoped temporary keys rather than real
// identity. Forge (internal/decisiongraph) validates the proposed
// dependency graph, assigns real identity, and writes Decision artifacts.
package planningsurvey

import (
	"context"
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/planningagent"
)

// invocationKey identifies the PlanningSurvey contract to a scripted
// planningagent.Backend (see FakeBackend.ProgramResult).
const invocationKey = "planning-survey"

// ProposedDecision is one Decision PlanningSurvey proposes.
//
// TempKey identifies the proposal only within this survey's Result -- it is
// not durable identity. Forge assigns each accepted proposal a real
// Decision ID (decisions/NNN-<slug>.md) when it materializes the graph.
//
// DependsOn lists the TempKeys of other proposals in the same Result that
// this Decision must be resolved after: a typed dependency edge PlanningSurvey
// proposes but does not itself validate.
//
// Consequential distinguishes a Decision worth tracking from a
// non-consequential unknown the survey noticed in passing (e.g. "we'll
// pick a logger later") that should never be materialized.
type ProposedDecision struct {
	TempKey       string   `json:"temp_key"`
	Title         string   `json:"title"`
	Question      string   `json:"question"`
	DependsOn     []string `json:"depends_on"`
	Consequential bool     `json:"consequential"`
}

// Result is PlanningSurvey's structured response.
type Result struct {
	Decisions []ProposedDecision `json:"decisions"`
}

// Propose runs the PlanningSurvey contract against backend for pc (expected
// to carry at least a Goal), returning the raw proposed Decisions. Propose
// performs no dependency-graph validation and writes nothing to disk -- see
// internal/decisiongraph.Materialize for that.
func Propose(ctx context.Context, backend planningagent.Backend, pc planningagent.PlanningContext) (Result, error) {
	return planningagent.InvokeStructured(ctx, backend, invocationKey, pc, buildPrompt, validateResult)
}

// buildPrompt renders pc's goal into the PlanningSurvey prompt. It carries
// no file or tracker mechanics: it reads only the typed PlanningContext
// already compiled by internal/planningagent.
func buildPrompt(pc planningagent.PlanningContext) string {
	var b strings.Builder
	b.WriteString("You are Forge's PlanningSurvey. Read the Feature goal below and propose the " +
		"Decisions that must be made before a spec can be written.\n\n" +
		"Only propose a Decision for a genuinely consequential unknown -- one that would " +
		"change the spec or the implementation depending on how it's answered. Do not " +
		"materialize incidental unknowns; note them as non-consequential (consequential: " +
		"false) instead of inventing a Decision for them.\n\n" +
		"Each proposed Decision needs a temp_key unique within your response, a title, the " +
		"question it must answer, and the temp_keys of any other proposed Decisions it " +
		"depends on (decisions that must be answered first).\n\n")

	if pc.Goal != nil {
		b.WriteString("## Goal\n\n")
		for _, heading := range []string{"Goal", "Summary", "Context"} {
			if body, ok := pc.Goal.Sections[heading]; ok && body != "" {
				fmt.Fprintf(&b, "### %s\n\n%s\n\n", heading, body)
			}
		}
	}

	if len(pc.Decisions) > 0 {
		b.WriteString("## Existing Decisions\n\n")
		for _, d := range pc.Decisions {
			fmt.Fprintf(&b, "- %s\n", d.ID)
		}
		b.WriteString("\n")
	}

	b.WriteString("Respond with exactly one fenced json block containing:\n" +
		`{"decisions":[{"temp_key":"...","title":"...","question":"...",` +
		`"depends_on":["..."],"consequential":true}]}` + "\n")

	return b.String()
}

// validateResult rejects a structured response InvokeStructured cannot
// safely hand to Forge for materialization: a blank or duplicate temp_key,
// or a blank title, on any proposed Decision. It does not validate the
// dependency graph itself (cycles, dangling depends_on) -- that is
// internal/decisiongraph.Materialize's job, since it needs Forge's fuller
// picture (existing Decisions, non-consequential filtering) to do so
// correctly.
func validateResult(res Result) error {
	return ValidateProposedDecisions(res.Decisions)
}

// ValidateProposedDecisions rejects a slice of ProposedDecision InvokeStructured
// cannot safely hand to Forge for materialization: a blank or duplicate
// temp_key, or a blank title, on any proposed Decision. It is exported so
// other planning contracts that surface ProposedDecision-shaped output (e.g.
// internal/decisionresolution's new_unknowns) can reuse the same rule rather
// than duplicating it.
func ValidateProposedDecisions(decisions []ProposedDecision) error {
	seen := make(map[string]bool, len(decisions))
	for i, d := range decisions {
		if d.TempKey == "" {
			return fmt.Errorf("planningsurvey: decision %d has a blank temp_key", i)
		}
		if seen[d.TempKey] {
			return fmt.Errorf("planningsurvey: duplicate temp_key %q", d.TempKey)
		}
		seen[d.TempKey] = true
		if d.Title == "" {
			return fmt.Errorf("planningsurvey: decision %q has a blank title", d.TempKey)
		}
	}
	return nil
}

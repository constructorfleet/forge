// Package decisionresolution implements the DecisionResolution planning
// contract (ticket 14): resolving exactly one ready Decision -- reading its
// question and whatever prior Decisions have already been resolved -- into
// an outcome, rationale, consequences, and assumptions, plus any new
// consequential unknowns the resolution surfaces. It is a typed call site
// built on internal/planningagent's structured-invocation core, mirroring
// internal/planningsurvey's shape.
//
// DecisionResolution performs no file or tracker mechanics: it only
// produces a Result. internal/decisiongraph applies that Result onto the
// target Decision Artifact and materializes any new_unknowns.
package decisionresolution

import (
	"context"
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/planningsurvey"
)

// invocationKey identifies the DecisionResolution contract to a scripted
// planningagent.Backend (see planningagent.FakeBackend.ProgramResult).
const invocationKey = "decision-resolution"

// Result is DecisionResolution's structured response: the resolved
// Decision's recorded content, and any new consequential unknowns the
// resolution surfaced. NewUnknowns reuses planningsurvey.ProposedDecision --
// the same DecisionProposal shape PlanningSurvey emits -- since both are
// forge's caller-scoped, not-yet-materialized proposal for a new Decision.
//
// NeedsHuman is populated instead of Outcome/Rationale/Consequences/
// Assumptions when the Decision cannot be resolved without a human's
// judgment -- something only a person can answer (a preference, an
// external constraint, an authorization). A NeedsHuman response never
// reaches internal/decisiongraph.ApplyResolution; see
// internal/decisiongraph.Pause (ticket 15a).
type Result struct {
	Outcome      string                            `json:"outcome"`
	Rationale    string                            `json:"rationale"`
	Consequences string                            `json:"consequences"`
	Assumptions  string                            `json:"assumptions"`
	NewUnknowns  []planningsurvey.ProposedDecision `json:"new_unknowns"`
	NeedsHuman   *NeedsHumanDetail                 `json:"needs_human"`
}

// NeedsHumanDetail describes, in structured form, what a NEEDS_HUMAN
// resolution requires from a human before the Decision can be resolved.
// Mirrors internal/agent.NeedsInfoDetail's shape for Phase 1 Issues.
type NeedsHumanDetail struct {
	Question string `json:"question"`
	Context  string `json:"context"`
}

// Request is DecisionResolution's typed input: a compiled PlanningContext
// and the ID of the one Decision (already present among Context.Decisions)
// to resolve this invocation.
type Request struct {
	Context  planningagent.PlanningContext
	TargetID string
}

// Resolve runs the DecisionResolution contract against backend for req,
// returning the raw resolution. It performs no dependency-graph mechanics
// and writes nothing to disk -- see internal/decisiongraph for that.
func Resolve(ctx context.Context, backend planningagent.Backend, req Request) (Result, error) {
	if req.TargetID == "" {
		return Result{}, fmt.Errorf("decisionresolution: target ID is blank")
	}
	if targetDecision(req) == nil {
		return Result{}, fmt.Errorf("decisionresolution: target %q not present in compiled PlanningContext", req.TargetID)
	}

	return planningagent.InvokeStructured(ctx, backend, invocationKey, req, buildPrompt, validateResult)
}

// targetDecision returns req.TargetID's ArtifactView from req.Context.Decisions,
// or nil if it is not present.
func targetDecision(req Request) *planningagent.ArtifactView {
	for i := range req.Context.Decisions {
		if req.Context.Decisions[i].ID == req.TargetID {
			return &req.Context.Decisions[i]
		}
	}
	return nil
}

// buildPrompt renders req's goal, the Decision to resolve, and any
// already-resolved Decisions (those with a non-blank Outcome section) into
// the DecisionResolution prompt. Each invocation is fresh: buildPrompt
// carries no memory of prior invocations beyond what req.Context compiles,
// matching ticket 14's "no reliance on prior conversation" requirement.
func buildPrompt(req Request) string {
	var b strings.Builder
	b.WriteString("You are Forge's DecisionResolution agent. Resolve exactly one Decision: " +
		"read its question below, and any already-resolved Decisions for context, then answer " +
		"it. Record your reasoning as rationale, note what your answer implies going forward as " +
		"consequences, and name anything you assumed. If answering surfaces a genuinely " +
		"consequential new unknown -- one that would change the spec or the implementation " +
		"depending on how it's answered -- propose it as a new_unknown; otherwise leave " +
		"new_unknowns empty. If the Decision genuinely cannot be resolved without a human's " +
		"judgment -- a preference, an external constraint, an authorization only a person can " +
		"give -- do not guess: respond with needs_human instead, and omit outcome, rationale, " +
		"consequences, assumptions, and new_unknowns.\n\n")

	pc := req.Context
	if pc.Goal != nil {
		b.WriteString("## Goal\n\n")
		for _, heading := range []string{"Goal", "Summary", "Context"} {
			if body, ok := pc.Goal.Sections[heading]; ok && body != "" {
				fmt.Fprintf(&b, "### %s\n\n%s\n\n", heading, body)
			}
		}
	}

	target := targetDecision(req)
	fmt.Fprintf(&b, "## Decision to resolve (%s)\n\n%s\n\n", req.TargetID, target.Sections["Question"])

	var resolved []planningagent.ArtifactView
	for _, d := range pc.Decisions {
		if d.ID == req.TargetID {
			continue
		}
		if outcome, ok := d.Sections["Outcome"]; ok && outcome != "" {
			resolved = append(resolved, d)
		}
	}
	if len(resolved) > 0 {
		b.WriteString("## Previously resolved decisions\n\n")
		for _, d := range resolved {
			fmt.Fprintf(&b, "- %s: %s\n", d.ID, d.Sections["Outcome"])
		}
		b.WriteString("\n")
	}

	b.WriteString("Respond with a JSON object containing either a resolution:\n" +
		`{"outcome":"...","rationale":"...","consequences":"...","assumptions":"...",` +
		`"new_unknowns":[{"temp_key":"...","title":"...","question":"...",` +
		`"depends_on":["..."],"consequential":true}]}` + "\n" +
		"or, if you need a human to decide:\n" +
		`{"needs_human":{"question":"...","context":"..."}}` + "\n")

	return b.String()
}

// validateResult rejects a structured response InvokeStructured cannot
// safely hand to Forge: a NeedsHuman response with a blank question, or --
// for a normal resolution -- a blank outcome, or a new_unknowns entry with
// a blank/duplicate temp_key or blank title (see
// planningsurvey.ValidateProposedDecisions).
func validateResult(res Result) error {
	if res.NeedsHuman != nil {
		if res.NeedsHuman.Question == "" {
			return fmt.Errorf("decisionresolution: needs_human question is blank")
		}
		return nil
	}
	if res.Outcome == "" {
		return fmt.Errorf("decisionresolution: outcome is blank")
	}
	return planningsurvey.ValidateProposedDecisions(res.NewUnknowns)
}

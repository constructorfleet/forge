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

	// GrillLoadBearing raises the bar for auto-resolving. When true, the
	// agent must defer a load-bearing Decision to the human (needs_human)
	// rather than choose for them -- a Decision is load-bearing when more
	// than one reasonable answer would materially change the specification
	// or the implementation and the goal does not determine which to pick.
	// Interactive `forge plan` sets this so a human at the TUI is grilled on
	// the choices that matter; a headless run leaves it false and stays
	// autonomous. A Decision that already carries human guidance (see
	// PlanningContext.HumanInputs) is never deferred, whatever this flag is.
	GrillLoadBearing bool
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

	// Human guidance takes precedence over the grill policy: a Decision the
	// human already answered must be resolved from that answer, never deferred
	// again. This is the resume path -- the human was grilled, answered, and
	// the loop re-resolves the Decision using their words.
	guidance := ""
	if pc.HumanInputs != nil {
		guidance = pc.HumanInputs[req.TargetID]
	}
	if guidance != "" {
		b.WriteString("A human has already answered this Decision. Resolve it from their " +
			"answer below: adopt their choice as the outcome, and record the rest " +
			"(rationale, consequences, assumptions) around it. Do NOT respond with " +
			"needs_human -- the human already decided.\n\n")
	} else if req.GrillLoadBearing {
		b.WriteString("This run is interactive: a human is watching and can answer. Defer " +
			"load-bearing Decisions to them. A Decision is load-bearing when more than one " +
			"reasonable answer would materially change the specification or the implementation " +
			"and the goal does not determine which to pick -- for those, respond with " +
			"needs_human rather than choosing yourself. Still resolve a Decision that is " +
			"mechanical, or that the goal and prior Decisions clearly determine.\n\n")
	}

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

	if guidance != "" {
		fmt.Fprintf(&b, "## Human's answer\n\n%s\n\n", guidance)
	}

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

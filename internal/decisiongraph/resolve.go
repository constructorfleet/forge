package decisiongraph

import (
	"github.com/Teagan42/forge/internal/decisionresolution"
	"github.com/Teagan42/forge/internal/planning"
)

// StateNeedsHuman is the Decision Artifact State Pause records: a Decision
// resolution reported NEEDS_HUMAN, so the Decision is neither resolved nor
// approved -- it stays off the frontier (see Frontier) until ticket 15b's
// resume clears it back to open.
const StateNeedsHuman = "needs_human"

// sectionOrder is the fixed order ApplyResolution writes a resolved
// Decision's sections in: the Question Materialize recorded, followed by
// the resolution's own content.
var sectionOrder = []string{"Question", "Outcome", "Rationale", "Consequences", "Assumptions"}

// ApplyResolution returns a copy of decision with res's outcome, rationale,
// consequences, and assumptions recorded into its Sections, and marked
// resolved and approved: Forge treats a successful DecisionResolution as the
// decision, so there is no separate human-approval step for it (unlike a
// blocking NEEDS_HUMAN Decision, which never reaches ApplyResolution --
// see ticket 15). ApplyResolution performs no I/O; callers persist the
// returned Artifact themselves.
func ApplyResolution(decision *planning.Artifact, res decisionresolution.Result) *planning.Artifact {
	content := map[string]string{
		"Question":     sectionBody(decision, "Question"),
		"Outcome":      res.Outcome,
		"Rationale":    res.Rationale,
		"Consequences": res.Consequences,
		"Assumptions":  res.Assumptions,
	}

	out := &planning.Artifact{
		Kind:        planning.KindDecision,
		State:       "resolved",
		DerivedFrom: append([]planning.DerivedFromEntry(nil), decision.DerivedFrom...),
	}
	for _, heading := range sectionOrder {
		if body := content[heading]; body != "" {
			out.Sections = append(out.Sections, planning.Section{Heading: heading, Body: body})
		}
	}

	out.Revision = planning.ComputeRevision(out)
	out.ApprovedRevision = out.Revision
	return out
}

// Pause returns a copy of decision recording that its resolution reported
// NEEDS_HUMAN: only its Question section is retained (nothing was actually
// resolved), its State is set to StateNeedsHuman, and it is left explicitly
// unapproved so planning.Ready (and therefore Frontier) never treats it as
// done. Because content is otherwise unchanged, Pause's recomputed Revision
// matches decision's own -- pausing carries no provenance consequence for
// dependents. Pause performs no I/O and posts nothing to a tracker; see
// internal/wayfinding for the checkpoint/tracker/runtime-status mechanics
// built on top of it (ticket 15a).
func Pause(decision *planning.Artifact) *planning.Artifact {
	out := &planning.Artifact{
		Kind:        planning.KindDecision,
		State:       StateNeedsHuman,
		DerivedFrom: append([]planning.DerivedFromEntry(nil), decision.DerivedFrom...),
	}
	if question := sectionBody(decision, "Question"); question != "" {
		out.Sections = append(out.Sections, planning.Section{Heading: "Question", Body: question})
	}
	out.Revision = planning.ComputeRevision(out)
	return out
}

// sectionBody returns decision's body for heading, or "" if it has no such
// section.
func sectionBody(decision *planning.Artifact, heading string) string {
	for _, s := range decision.Sections {
		if s.Heading == heading {
			return s.Body
		}
	}
	return ""
}

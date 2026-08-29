package decisiongraph

import (
	"github.com/Teagan42/forge/internal/decisionresolution"
	"github.com/Teagan42/forge/internal/planning"
)

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

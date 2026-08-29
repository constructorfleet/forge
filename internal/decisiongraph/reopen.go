package decisiongraph

import (
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningsurvey"
)

// StateReplanRequired is the Decision Artifact State a replan trigger
// records: an implementation Worker found the governing plan invalid, so
// this Decision is open again and must be re-resolved and re-approved before
// the Feature's frozen work can resume.
const StateReplanRequired = "replan_required"

// replanTriggerHeading is the section a replan trigger is recorded under.
// It is definitional content (planning.ComputeRevision hashes Sections), so
// writing it is exactly what makes the Decision's revision move — which is
// how every downstream artifact derived from the old revision starts
// evaluating as stale, with no stored staleness bit anywhere.
const replanTriggerHeading = "Replan Trigger"

// reportedByPrefix is the line inside the Replan Trigger section that names
// the Issue whose Worker escalated. FindReplanDecision matches on it, so a
// repeated escalation from the same Issue reopens the same Decision instead
// of accumulating near-duplicates.
const reportedByPrefix = "Reported by issue: "

// ReplanTrigger is one REPLAN_REQUIRED escalation in the shape the Decision
// graph consumes it: what the Worker found, the evidence for it, which
// requirements it reaches, the planning question it proposes, and the ticket
// plan revision the Worker was executing under when it found the problem.
type ReplanTrigger struct {
	IssueID              string
	Reason               string
	Evidence             string
	AffectedRequirements []string
	SuggestedQuestion    string
	PlanRevision         string
}

// Question is the planning question the trigger raises: the Agent's own
// SuggestedQuestion when it supplied one, otherwise the Reason restated as
// the thing to decide.
func (t ReplanTrigger) Question() string {
	if q := strings.TrimSpace(t.SuggestedQuestion); q != "" {
		return q
	}
	return "How should the plan change given: " + strings.TrimSpace(t.Reason) + "?"
}

// title is the human-facing title the materialized Decision's ID slug is
// derived from.
func (t ReplanTrigger) title() string {
	return "Replan " + strings.TrimSpace(t.Reason)
}

// MaterializeReplanTrigger creates a fresh Decision Artifact for trigger,
// assigning it an ID through the same NNN-slug machinery Materialize uses
// (so a replan Decision is numbered and slugged exactly like a survey one
// and can never collide with existingIDs). The Decision is created open —
// State StateReplanRequired, ApprovedRevision unset — so planning.Ready is
// false for it and nothing downstream may rely on it until it is resolved
// and approved.
//
// MaterializeReplanTrigger performs no I/O; callers render
// (planning.Render) and write the returned Artifact themselves.
func MaterializeReplanTrigger(trigger ReplanTrigger, goal GoalRef, existingIDs []string) (MaterializedDecision, error) {
	if strings.TrimSpace(trigger.Reason) == "" {
		return MaterializedDecision{}, fmt.Errorf("decisiongraph: replan trigger has a blank reason")
	}

	materialized, err := Materialize([]planningsurvey.ProposedDecision{{
		TempKey:       "replan",
		Title:         trigger.title(),
		Question:      trigger.Question(),
		Consequential: true,
	}}, goal, existingIDs)
	if err != nil {
		return MaterializedDecision{}, err
	}
	if len(materialized) != 1 {
		return MaterializedDecision{}, fmt.Errorf("decisiongraph: replan trigger materialized %d decisions, want 1", len(materialized))
	}

	out := materialized[0]
	out.TempKey = ""
	out.Artifact.State = StateReplanRequired
	out.Artifact.Sections = append(out.Artifact.Sections, planning.Section{
		Heading: replanTriggerHeading,
		Body:    renderReplanTrigger(trigger),
	})
	out.Artifact.Revision = planning.ComputeRevision(out.Artifact)
	return out, nil
}

// Reopen returns a copy of decision reopened by trigger: its existing
// content is kept verbatim (a reopened Decision must not lose the reasoning
// that produced the work already completed under it), the trigger is
// recorded as/into its Replan Trigger section, its State becomes
// StateReplanRequired, and its ApprovedRevision is dropped so
// planning.Approved — and therefore planning.Ready and the frontier — treat
// it as open again.
//
// Because the trigger is definitional content, the recomputed Revision
// necessarily differs from the one downstream artifacts recorded in their
// DerivedFrom entries: those artifacts evaluate as stale purely through
// provenance comparison. Nothing sets a staleness flag, here or anywhere.
//
// Reopen performs no I/O; callers persist the returned Artifact themselves.
func Reopen(decision *planning.Artifact, trigger ReplanTrigger) *planning.Artifact {
	out := &planning.Artifact{
		Kind:        decision.Kind,
		State:       StateReplanRequired,
		DerivedFrom: append([]planning.DerivedFromEntry(nil), decision.DerivedFrom...),
		Estimates:   decision.Estimates,
	}

	body := renderReplanTrigger(trigger)
	replaced := false
	for _, s := range decision.Sections {
		if s.Heading == replanTriggerHeading {
			// A Decision already carrying a trigger accumulates the new one
			// beneath it: the earlier escalation is history that stays
			// readable, not something the second escalation overwrites.
			out.Sections = append(out.Sections, planning.Section{
				Heading: replanTriggerHeading,
				Body:    strings.TrimRight(s.Body, "\n") + "\n\n" + body,
			})
			replaced = true
			continue
		}
		out.Sections = append(out.Sections, s)
	}
	if !replaced {
		out.Sections = append(out.Sections, planning.Section{Heading: replanTriggerHeading, Body: body})
	}

	out.Revision = planning.ComputeRevision(out)
	return out
}

// FindReplanDecision returns the ID of the Decision in decisions that
// already records a replan trigger reported by issueID, if any. It is what
// makes recording a trigger idempotent: a Worker that escalates the same
// Issue twice reopens one Decision rather than creating a second.
func FindReplanDecision(decisions map[string]*planning.Artifact, issueID string) (string, bool) {
	if issueID == "" {
		return "", false
	}
	needle := reportedByPrefix + issueID
	var found string
	for id, artifact := range decisions {
		if artifact == nil {
			continue
		}
		for _, s := range artifact.Sections {
			if s.Heading != replanTriggerHeading {
				continue
			}
			for _, line := range strings.Split(s.Body, "\n") {
				if strings.TrimSpace(line) != needle {
					continue
				}
				// Deterministic pick: the lowest ID wins if a hand-edit ever
				// left two Decisions naming the same reporting Issue.
				if found == "" || id < found {
					found = id
				}
			}
		}
	}
	return found, found != ""
}

// renderReplanTrigger renders trigger as the Replan Trigger section body.
// The field order is fixed so the same trigger always produces the same
// bytes and therefore the same content revision.
func renderReplanTrigger(trigger ReplanTrigger) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s\n", reportedByPrefix, trigger.IssueID)
	if trigger.PlanRevision != "" {
		fmt.Fprintf(&b, "Ticket plan revision: %s\n", trigger.PlanRevision)
	}
	fmt.Fprintf(&b, "Reason: %s\n", strings.TrimSpace(trigger.Reason))
	if evidence := strings.TrimSpace(trigger.Evidence); evidence != "" {
		fmt.Fprintf(&b, "Evidence: %s\n", evidence)
	}
	if len(trigger.AffectedRequirements) > 0 {
		fmt.Fprintf(&b, "Affected requirements: %s\n", strings.Join(trigger.AffectedRequirements, ", "))
	}
	if q := strings.TrimSpace(trigger.SuggestedQuestion); q != "" {
		fmt.Fprintf(&b, "Suggested question: %s\n", q)
	}
	return strings.TrimRight(b.String(), "\n")
}

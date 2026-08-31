package engine

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/tracker"
)

// FollowUpTracker is the subset of tracker.Tracker automatic self reporting
// needs: creating a new tracker Issue for each agent.FollowUpReport and
// labeling it so it lands in the same triage queue a human-filed Issue
// would (see docs/agents/triage-labels.md). Depending on this narrow
// interface rather than tracker.Tracker keeps the handling backend-agnostic
// and its test double small, matching NeedsInfoTracker's rationale.
type FollowUpTracker interface {
	CreateIssue(ctx context.Context, req tracker.IssueRequest) (tracker.CreatedIssue, error)
	AddLabel(ctx context.Context, id string, label string) error
}

// reportFollowUps implements automatic self reporting (issue 141): for
// each out-of-scope observation the Agent returned alongside its result, it
// files a new tracker Issue backlinked to the originating Issue and applies
// the configured triage label. It is best-effort and orthogonal to the
// Issue's own outcome — reportFollowUps runs regardless of Status, and a
// failure to file one follow-up (a tracker error) is recorded as an event
// rather than failing the Worker, since the Agent's actual work already
// succeeded or failed on its own terms independent of this side channel.
// A nil FollowUpTracker (not wired) or an empty follow-ups list makes this
// a no-op, matching NeedsInfoTracker's optional-field convention.
func (e *Engine) reportFollowUps(ctx context.Context, executionID, issueID string, followUps []agent.FollowUpReport) error {
	if e.FollowUpTracker == nil || len(followUps) == 0 {
		return nil
	}

	label := e.Config.FollowUp.Label

	for _, f := range followUps {
		body := fmt.Sprintf("%s\n\n---\nNoticed by Forge while working #%s.", f.Body, issueID)
		created, err := e.FollowUpTracker.CreateIssue(ctx, tracker.IssueRequest{Title: f.Title, Body: body})
		if err != nil {
			if evErr := e.appendEvent(ctx, executionID, issueID, "followup.failed", map[string]string{
				"title": f.Title,
				"error": err.Error(),
			}); evErr != nil {
				return evErr
			}
			continue
		}

		if label != "" {
			if err := e.FollowUpTracker.AddLabel(ctx, created.ID, label); err != nil {
				if evErr := e.appendEvent(ctx, executionID, issueID, "followup.failed", map[string]string{
					"title": f.Title,
					"error": err.Error(),
				}); evErr != nil {
					return evErr
				}
				continue
			}
		}

		if err := e.appendEvent(ctx, executionID, issueID, "followup.created", map[string]string{
			"title":         f.Title,
			"issue_id":      created.ID,
			"issue_url":     created.URL,
			"reported_from": issueID,
		}); err != nil {
			return err
		}
	}

	return nil
}

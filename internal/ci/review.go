package ci

import (
	"context"
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/statusreflect"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// pollReviews checks pull request number's review feedback (issue 109,
// "Review Rectification"). It is a no-op — handled false, no error — when
// s.Tracker doesn't implement tracker.ReviewsGetter. A single reviewer's
// actionable CHANGES_REQUESTED review is recorded as a CIRun (Kind:
// review) and transitions the Issue to CI_FAILED, exactly like a failed
// required check — it re-enters the same repair loop and the same
// retry.CI budget engine.RepairCIFailure already enforces (CONTEXT.md
// "Retry Budget"). Ambiguous feedback (more than one reviewer currently
// requesting changes) is routed to NEEDS_INFO instead; feedback with
// nothing actionable (an approval, a comment, or a bare CHANGES_REQUESTED
// with no stated reason) leaves the Issue polling.
func (s *Supervisor) pollReviews(ctx context.Context, executionID, issueID string, number int) (handled bool, state domain.IssueState, err error) {
	getter, ok := s.Tracker.(tracker.ReviewsGetter)
	if !ok {
		return false, "", nil
	}

	reviews, err := getter.GetPullRequestReviews(ctx, number)
	if err != nil {
		return true, "", fmt.Errorf("ci: poll reviews for issue %s: %w", issueID, err)
	}

	verdict, actionable, ambiguousAuthors := classifyReviews(reviews)
	switch verdict {
	case reviewVerdictNone:
		return false, "", nil

	case reviewVerdictAmbiguous:
		run := storage.CIRun{
			ExecutionID: executionID,
			IssueID:     issueID,
			Status:      storage.CIRunStatusFailed,
			Kind:        storage.CIRunKindReview,
			Details:     "multiple reviewers requested changes: " + strings.Join(ambiguousAuthors, ", "),
			CheckedAt:   s.Now(),
		}
		if err := s.Store.RecordCIRun(ctx, run); err != nil {
			return true, "", fmt.Errorf("ci: persist run for issue %s: %w", issueID, err)
		}
		state, err = s.routeToNeedsInfo(ctx, executionID, issueID,
			"Multiple reviewers requested changes on this pull request and Forge cannot safely reconcile the feedback automatically.",
			"Reviewers requesting changes: "+strings.Join(ambiguousAuthors, ", ")+". Please clarify which direction to take, or have the reviewers align.",
		)
		return true, state, err

	default: // reviewVerdictActionable
		run := storage.CIRun{
			ExecutionID: executionID,
			IssueID:     issueID,
			Status:      storage.CIRunStatusFailed,
			Kind:        storage.CIRunKindReview,
			CheckName:   actionable.Author,
			Details:     capDetails(actionable.Body, s.Config.CI.MaxOutputBytes),
			CheckedAt:   s.Now(),
		}
		if err := s.Store.RecordCIRun(ctx, run); err != nil {
			return true, "", fmt.Errorf("ci: persist run for issue %s: %w", issueID, err)
		}
		if handled, state, err := s.handlePublishedConflictCandidateFailure(ctx, executionID, issueID, "review from "+actionable.Author+" requested changes: "+actionable.Body); handled || err != nil {
			return handled, state, err
		}
		issue, err := s.Store.TransitionIssue(ctx, executionID, issueID, domain.StateCIFailed)
		if err != nil {
			return true, "", fmt.Errorf("ci: transition issue %s to CI_FAILED: %w", issueID, err)
		}
		if err := statusreflect.Apply(ctx, s.StatusTracker, s.Config.StatusReflection, issueID, domain.StateCIPending, domain.StateCIFailed); err != nil {
			return true, "", fmt.Errorf("ci: reflect status for issue %s: %w", issueID, err)
		}
		return true, issue.State, nil
	}
}

type reviewVerdict int

const (
	reviewVerdictNone reviewVerdict = iota
	reviewVerdictActionable
	reviewVerdictAmbiguous
)

// classifyReviews reduces a pull request's raw review history to a single
// verdict: reduce to each author's most recent non-dismissed review, then
// look at how many of those latest reviews are CHANGES_REQUESTED.
//
//   - Zero -> reviewVerdictNone: nothing actionable (approvals, comments,
//     or no reviews at all).
//   - Exactly one, with a non-empty Body -> reviewVerdictActionable: one
//     reviewer's concrete feedback, safe to hand to the repair agent
//     unsupervised.
//   - Exactly one, with an empty Body -> reviewVerdictNone: a bare
//     "changes requested" with no stated reason gives the repair agent
//     nothing to act on, so it is left pending rather than guessed at.
//   - More than one -> reviewVerdictAmbiguous: conflicting or overlapping
//     change requests from different reviewers require a human to
//     reconcile, not an agent improvising which one to follow.
func classifyReviews(reviews []tracker.PullRequestReview) (reviewVerdict, tracker.PullRequestReview, []string) {
	latest := map[string]tracker.PullRequestReview{}
	var order []string
	for _, r := range reviews {
		if r.State == tracker.ReviewDismissed {
			delete(latest, r.Author)
			continue
		}
		if _, seen := latest[r.Author]; !seen {
			order = append(order, r.Author)
		}
		latest[r.Author] = r
	}

	var changesRequested []tracker.PullRequestReview
	for _, author := range order {
		if rv := latest[author]; rv.State == tracker.ReviewChangesRequested {
			changesRequested = append(changesRequested, rv)
		}
	}

	switch {
	case len(changesRequested) == 0:
		return reviewVerdictNone, tracker.PullRequestReview{}, nil
	case len(changesRequested) > 1:
		authors := make([]string, len(changesRequested))
		for i, rv := range changesRequested {
			authors[i] = rv.Author
		}
		return reviewVerdictAmbiguous, tracker.PullRequestReview{}, authors
	case strings.TrimSpace(changesRequested[0].Body) == "":
		return reviewVerdictNone, tracker.PullRequestReview{}, nil
	default:
		return reviewVerdictActionable, changesRequested[0], nil
	}
}

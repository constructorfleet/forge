package ci

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// pollConflict checks pull request number's mergeability against its base
// branch (issue 109, "Merge Conflicts"). It is a no-op — handled false, no
// error — when s.Tracker doesn't implement tracker.MergeStatusGetter.
// A detected conflict is recorded as a CIRun (Kind: conflict) and routed to
// NEEDS_INFO: Forge attempts no automatic conflict resolution (the
// requirement's "may attempt... where supported" is not yet supported), so
// every conflict stops the run for a human rather than guessing at a
// resolution.
func (s *Supervisor) pollConflict(ctx context.Context, executionID, issueID string, number int) (handled bool, state domain.IssueState, err error) {
	getter, ok := s.Tracker.(tracker.MergeStatusGetter)
	if !ok {
		return false, "", nil
	}

	status, err := getter.GetPullRequestMergeStatus(ctx, number)
	if err != nil {
		return true, "", fmt.Errorf("ci: poll merge status for issue %s: %w", issueID, err)
	}
	if !status.Conflicted {
		return false, "", nil
	}

	run := storage.CIRun{
		ExecutionID: executionID,
		IssueID:     issueID,
		Status:      storage.CIRunStatusFailed,
		Kind:        storage.CIRunKindConflict,
		Details:     "pull request cannot be merged into its base branch due to a conflict",
		CheckedAt:   s.Now(),
	}
	if err := s.Store.RecordCIRun(ctx, run); err != nil {
		return true, "", fmt.Errorf("ci: persist run for issue %s: %w", issueID, err)
	}

	state, err = s.routeToNeedsInfo(ctx, executionID, issueID,
		"This pull request has a merge conflict with its base branch that Forge cannot resolve automatically.",
		"Resolve the conflict (e.g. rebase or merge the base branch into the pull request branch) and push the update, or comment with guidance.",
	)
	return true, state, err
}

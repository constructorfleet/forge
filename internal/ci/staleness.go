package ci

import (
	"context"
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// Rebaser is the workspace-level capability Wait uses to move a stale pull
// request's Workspace branch onto its target branch (issue 233) before
// trusting checks evaluated against a base GitHub already considers
// out of date. Structurally identical to engine.WorkspaceRebaser;
// duplicated narrowly here for the same reason as NeedsInfoTracker
// (internal/ci must not import internal/engine — see NeedsInfoTracker's
// doc comment).
//
// A conflict-free rebase returns (nil, nil). A rebase that hits a
// conflict is aborted (the Workspace is left exactly as it was) and its
// conflicting paths are returned with a nil error — a conflict is an
// expected, caller-actionable outcome, not an infrastructure failure.
type Rebaser interface {
	Rebase(ctx context.Context, executionID, issueID, newBase string) (conflictPaths []string, err error)
}

// BranchPusher pushes a Workspace's rebased branch back to its tracker
// remote. Unlike engine.Publisher.Push (an ordinary, fast-forward-only
// push used once after COMMITTING), Rebase moves the branch's tip
// non-fast-forward relative to whatever was previously pushed, so
// ForcePush must force the remote ref to match the rebased local branch
// (e.g. `git push --force-with-lease`).
type BranchPusher interface {
	ForcePush(ctx context.Context, workspacePath, branch string) error
}

// BranchResetter moves a Workspace branch back to a known commit after an
// automatic conflict-resolution candidate fails before publication.
type BranchResetter interface {
	Reset(ctx context.Context, workspacePath, commitSHA string) error
}

// ConflictBranchRestorer restores a published automatic conflict-repair
// candidate back to the original pull-request head using an explicit lease
// on the candidate SHA, then restores the live Workspace branch locally.
type ConflictBranchRestorer interface {
	EnsureWorkspaceReady(ctx context.Context, workspacePath string) error
	ForcePushCommitWithLease(ctx context.Context, workspacePath, branch, commitSHA, expectedRemoteSHA string) error
	Reset(ctx context.Context, workspacePath, commitSHA string) error
}

// pollStale checks pull request number's mergeability against its base
// branch for staleness (GitHub's mergeable_state == "behind"), using the
// merge status Wait already fetched once this poll (see
// Supervisor.mergeStatus), and, when stale, rebases the Issue's Workspace
// onto s.BaseBranch and force-pushes the result so the pull request — and
// the checks Wait polls next — reflect the target branch's current tip.
//
// It is a no-op — handled false, no error — when haveStatus is false
// (s.Tracker doesn't implement tracker.MergeStatusGetter), when
// s.Rebaser/s.Pusher are not configured, or when the tracker doesn't report
// the pull request as behind: Wait then falls back to its pre-issue-233
// behavior of polling checks against whatever state the PR is already in,
// exactly like pollConflict/pollReviews do for their own optional
// capabilities.
//
// A conflict-free rebase+push is not itself a terminal outcome for the
// Issue: Wait keeps polling (handled false) so the very next step in this
// same iteration evaluates checks against the refreshed branch. A rebase
// conflict is routed to NEEDS_INFO instead, exactly like pollConflict's
// unresolvable merge conflict — Forge attempts no automatic conflict
// resolution.
func (s *Supervisor) pollStale(ctx context.Context, executionID, issueID string, number int, status tracker.PullRequestMergeStatus, haveStatus bool) (handled bool, state domain.IssueState, err error) {
	if !haveStatus || s.Rebaser == nil || s.Pusher == nil || !status.Behind {
		return false, "", nil
	}

	ws, err := s.Store.WorkspaceByIssue(ctx, executionID, issueID)
	if err != nil {
		return true, "", fmt.Errorf("ci: load workspace for issue %s: %w", issueID, err)
	}

	conflicts, err := s.Rebaser.Rebase(ctx, executionID, issueID, s.BaseBranch)
	if err != nil {
		return true, "", fmt.Errorf("ci: rebase issue %s onto %s: %w", issueID, s.BaseBranch, err)
	}
	if len(conflicts) > 0 {
		run := storage.CIRun{
			ExecutionID: executionID,
			IssueID:     issueID,
			Status:      storage.CIRunStatusFailed,
			Kind:        storage.CIRunKindStale,
			Details:     "rebase onto " + s.BaseBranch + " conflicted: " + strings.Join(conflicts, ", "),
			CheckedAt:   s.Now(),
		}
		if err := s.Store.RecordCIRun(ctx, run); err != nil {
			return true, "", fmt.Errorf("ci: persist run for issue %s: %w", issueID, err)
		}
		state, err = s.routeToNeedsInfo(ctx, executionID, issueID,
			"This pull request fell behind its base branch and Forge's automatic rebase hit a conflict it cannot resolve.",
			"Resolve the conflict (e.g. rebase manually onto "+s.BaseBranch+") and push the update, or comment with guidance.",
		)
		return true, state, err
	}

	if err := s.Pusher.ForcePush(ctx, ws.Path, ws.Branch); err != nil {
		return true, "", fmt.Errorf("ci: force-push rebased branch %s for issue %s: %w", ws.Branch, issueID, err)
	}

	run := storage.CIRun{
		ExecutionID: executionID,
		IssueID:     issueID,
		Status:      storage.CIRunStatusPassed,
		Kind:        storage.CIRunKindStale,
		Details:     "rebased onto " + s.BaseBranch,
		CheckedAt:   s.Now(),
	}
	if err := s.Store.RecordCIRun(ctx, run); err != nil {
		return true, "", fmt.Errorf("ci: persist run for issue %s: %w", issueID, err)
	}

	return false, "", nil
}

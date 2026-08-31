package ci

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// ConflictResolutionRequest carries the persisted pull-request and issue
// facts a ConflictResolver needs to attempt ADR-0017's bounded automatic
// conflict repair.
type ConflictResolutionRequest struct {
	ExecutionID        string
	IssueID            string
	PullRequestNumber  int
	BaseBranch         string
	PullRequestHeadSHA string
}

// ConflictResolutionResult reports whether automatic merge-conflict repair
// produced and published a validated candidate. Details is persisted in the
// CI audit trail and should be suitable for a human reading the Issue.
type ConflictResolutionResult struct {
	Resolved bool
	Details  string
}

// ConflictResolver is the optional capability the CI Supervisor uses to
// attempt ADR-0017 automatic conflict repair before routing to NEEDS_INFO.
type ConflictResolver interface {
	ResolveMergeConflict(ctx context.Context, req ConflictResolutionRequest) (ConflictResolutionResult, error)
}

// pollConflict checks pull request number's mergeability against its base
// branch (issue 109, "Merge Conflicts"), using the merge status Wait already
// fetched once this poll (see Supervisor.mergeStatus). It is a no-op —
// handled false, no error — when haveStatus is false (s.Tracker doesn't
// implement tracker.MergeStatusGetter). A detected conflict is first offered
// to ConflictResolver when configured. A successful automatic repair records
// a passed conflict CIRun and lets Wait continue normal CI supervision; an
// unconfigured or unresolved conflict is recorded as failed and routed to
// NEEDS_INFO.
func (s *Supervisor) pollConflict(ctx context.Context, executionID, issueID string, pr storage.PullRequest, status tracker.PullRequestMergeStatus, haveStatus bool) (handled bool, state domain.IssueState, err error) {
	if !haveStatus || !status.Conflicted {
		return false, "", nil
	}

	if s.ConflictResolver != nil {
		if pr.CommitSHA == "" {
			return s.routeUnresolvedConflict(ctx, executionID, issueID, "automatic conflict replay refused: recorded pull request head SHA is empty")
		}
		result, err := s.ConflictResolver.ResolveMergeConflict(ctx, ConflictResolutionRequest{
			ExecutionID:        executionID,
			IssueID:            issueID,
			PullRequestNumber:  pr.Number,
			BaseBranch:         s.BaseBranch,
			PullRequestHeadSHA: pr.CommitSHA,
		})
		if err != nil {
			return true, "", fmt.Errorf("ci: resolve merge conflict for issue %s: %w", issueID, err)
		}
		if result.Resolved {
			run := storage.CIRun{
				ExecutionID: executionID,
				IssueID:     issueID,
				Status:      storage.CIRunStatusPassed,
				Kind:        storage.CIRunKindConflict,
				Details:     capDetails(result.Details, s.Config.CI.MaxOutputBytes),
				CheckedAt:   s.Now(),
			}
			if err := s.Store.RecordCIRun(ctx, run); err != nil {
				return true, "", fmt.Errorf("ci: persist run for issue %s: %w", issueID, err)
			}
			return false, "", nil
		}
		if result.Details != "" {
			return s.routeUnresolvedConflict(ctx, executionID, issueID, result.Details)
		}
	}

	return s.routeUnresolvedConflict(ctx, executionID, issueID, "pull request cannot be merged into its base branch due to a conflict")
}

func (s *Supervisor) routeUnresolvedConflict(ctx context.Context, executionID, issueID, details string) (handled bool, state domain.IssueState, err error) {
	run := storage.CIRun{
		ExecutionID: executionID,
		IssueID:     issueID,
		Status:      storage.CIRunStatusFailed,
		Kind:        storage.CIRunKindConflict,
		Details:     capDetails(details, s.Config.CI.MaxOutputBytes),
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

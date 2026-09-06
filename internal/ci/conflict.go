package ci

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/prbase"
	"github.com/Teagan42/forge/internal/statusreflect"
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
// attempt ADR-0017 automatic conflict repair before routing an unresolvable
// conflict into the CI repair loop (CI_FAILED, bounded by the Issue's CI
// retry budget), exactly like a failed required check or an actionable
// review (see review.go): the resolver's Git replay is the bounded first
// attempt, and a refusal is a repairable CI failure, not a terminal
// human-blocking NEEDS_INFO.
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
// CI_FAILED so the scheduler's CIRepairer re-runs the Worker against the
// current base and the Agent reconciles the conflict itself (ADR 0017,
// amended for the repair path).
func (s *Supervisor) pollConflict(ctx context.Context, executionID, issueID string, pr storage.PullRequest, status tracker.PullRequestMergeStatus, haveStatus bool) (handled bool, state domain.IssueState, err error) {
	if !haveStatus || !status.Conflicted {
		return false, "", nil
	}

	if s.ConflictResolver != nil {
		if pr.CommitSHA == "" {
			return s.routeUnresolvedConflict(ctx, executionID, issueID, "automatic conflict replay refused: recorded pull request head SHA is empty")
		}
		baseBranch, err := s.conflictResolutionBaseBranch(ctx, executionID, issueID, pr)
		if err != nil {
			return true, "", err
		}
		result, err := s.ConflictResolver.ResolveMergeConflict(ctx, ConflictResolutionRequest{
			ExecutionID:        executionID,
			IssueID:            issueID,
			PullRequestNumber:  pr.Number,
			BaseBranch:         baseBranch,
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

func (s *Supervisor) conflictResolutionBaseBranch(ctx context.Context, executionID, issueID string, pr storage.PullRequest) (string, error) {
	if getter, ok := s.Tracker.(tracker.PullRequestTargetBranchGetter); ok {
		base, err := getter.GetPullRequestTargetBranch(ctx, pr.Number)
		if err != nil {
			return "", fmt.Errorf("ci: resolve current pull request target for issue %s: %w", issueID, err)
		}
		if base != "" {
			return base, nil
		}
	}
	if pr.BaseBranch != "" {
		return pr.BaseBranch, nil
	}
	issue, err := s.Store.GetIssue(ctx, executionID, issueID)
	if err != nil {
		return "", fmt.Errorf("ci: resolve conflict base for issue %s: %w", issueID, err)
	}
	base, err := prbase.Resolve(ctx, s.Store, executionID, issue, s.BaseBranch)
	if err != nil {
		return "", fmt.Errorf("ci: resolve conflict base for issue %s: %w", issueID, err)
	}
	return base, nil
}

// routeUnresolvedConflict records a failed conflict CIRun (Details carries
// the refusal reason — the conflicting path list or the resolver's message)
// and routes the Issue into the CI repair loop via routeIssueToCIRepair,
// instead of the former terminal NEEDS_INFO. The persisted failed run is
// exactly the diagnostic engine.RepairCIFailure's latestFailedCIRun hands a
// repair Agent, so a refused conflict is re-worked against the current base
// (see buildCIFeedback's conflict framing) rather than parked forever as a
// human decision.
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

	state, err = s.routeIssueToCIRepair(ctx, executionID, issueID)
	return true, state, err
}

// routeIssueToCIRepair transitions an Issue already in CI_PENDING to
// CI_FAILED after the observed CI failure could not be auto-repaired by the
// poll-layer remedy attached to it (an unresolvable merge conflict, or a
// stale-PR rebase that hit conflicts), so the scheduler's CIRepairer
// (engine.RepairCIFailure) re-runs the Worker against the refreshed base and
// the Agent reconciles the conflict itself, bounded by the Issue's CI retry
// budget. This mirrors the actionable-review path (review.go) and the
// failed-required-check path (supervisor.go) exactly: a PR that cannot merge
// is a repairable CI failure, and only the ambiguous or ownership-lost cases
// that remain after repair's budget is spent land in a terminal state. The
// caller records the failed CIRun before calling this; the transition and
// status reflection are the shared remainder.
func (s *Supervisor) routeIssueToCIRepair(ctx context.Context, executionID, issueID string) (domain.IssueState, error) {
	issue, err := s.Store.TransitionIssue(ctx, executionID, issueID, domain.StateCIFailed)
	if err != nil {
		return "", fmt.Errorf("ci: transition issue %s to CI_FAILED: %w", issueID, err)
	}
	if err := statusreflect.Apply(ctx, s.StatusTracker, s.Config.StatusReflection, issueID, domain.StateCIPending, domain.StateCIFailed); err != nil {
		return "", fmt.Errorf("ci: reflect status for issue %s: %w", issueID, err)
	}
	return issue.State, nil
}

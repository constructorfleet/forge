package prbase

import (
	"context"
	"errors"
	"fmt"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

// Store loads the Issue and Workspace data needed to choose a pull request
// base.
type Store interface {
	GetIssue(ctx context.Context, executionID, issueID string) (domain.Issue, error)
	WorkspaceByIssue(ctx context.Context, executionID, issueID string) (domain.Workspace, error)
}

// Resolve returns the branch a pull request should target.
//
// An Issue with exactly one Dependency stacks on that prerequisite's branch,
// so review shows only the child's own diff. An Issue with zero Dependencies,
// or more than one Dependency, targets the base branch. An unknown
// prerequisite (no Workspace recorded, for example an External Dependency)
// also targets the base branch.
//
// Resolve keeps the recorded prerequisite branch even after the prerequisite
// merges. Use Resolve to re-derive the base of a pull request that already
// exists (for example, conflict resolution), where the host has already
// migrated the live target. To choose the base for a new pull request, use
// ResolveForNewPullRequest.
func Resolve(ctx context.Context, store Store, executionID string, issue domain.Issue, baseBranch string) (string, error) {
	if len(issue.Dependencies) != 1 {
		return baseBranch, nil
	}
	ws, err := store.WorkspaceByIssue(ctx, executionID, issue.Dependencies[0].DependsOnID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return baseBranch, nil
		}
		return "", fmt.Errorf("resolve prerequisite branch for issue %s: %w", issue.ID, err)
	}
	return ws.Branch, nil
}

// ResolveForNewPullRequest returns the branch a new pull request should
// target. It is Resolve with one more rule for a pull request that does not
// exist yet: it targets the base branch when the single prerequisite has
// already merged (the prerequisite Issue is DONE).
//
// The host deletes the prerequisite branch when the prerequisite pull request
// merges, so a new pull request that targets the deleted branch fails to
// open. The prerequisite's commits are already reachable from the base
// branch, so the child's diff against the base branch is correct.
func ResolveForNewPullRequest(ctx context.Context, store Store, executionID string, issue domain.Issue, baseBranch string) (string, error) {
	if len(issue.Dependencies) == 1 {
		prereq, err := store.GetIssue(ctx, executionID, issue.Dependencies[0].DependsOnID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return baseBranch, nil
			}
			return "", fmt.Errorf("resolve prerequisite state for issue %s: %w", issue.ID, err)
		}
		if prereq.State == domain.StateDone {
			return baseBranch, nil
		}
	}
	return Resolve(ctx, store, executionID, issue, baseBranch)
}

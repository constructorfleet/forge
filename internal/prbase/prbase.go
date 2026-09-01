package prbase

import (
	"context"
	"errors"
	"fmt"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

// Store loads the Workspace data needed to choose a pull request base.
type Store interface {
	WorkspaceByIssue(ctx context.Context, executionID, issueID string) (domain.Workspace, error)
}

// Resolve returns the branch a new pull request should target.
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

package ci

import (
	"context"
	"errors"
	"fmt"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

func (s *Supervisor) handlePublishedConflictCandidateFailure(ctx context.Context, executionID, issueID, reason string) (handled bool, state domain.IssueState, err error) {
	attempt, err := s.Store.ActiveConflictResolutionAttempt(ctx, executionID, issueID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return false, "", nil
		}
		return true, "", err
	}

	ws, err := s.Store.WorkspaceByIssue(ctx, executionID, issueID)
	if err != nil {
		return true, "", fmt.Errorf("ci: load workspace for issue %s: %w", issueID, err)
	}

	details := fmt.Sprintf("automatic conflict replay candidate %s failed after publication: %s", attempt.CandidateSHA, reason)
	status := storage.ConflictResolutionStatusRestored
	if s.ConflictRestorer == nil {
		status = storage.ConflictResolutionStatusLostLease
		details += "; no conflict branch restorer is configured"
	} else if err := s.ConflictRestorer.EnsureWorkspaceReady(ctx, ws.Path); err != nil {
		status = storage.ConflictResolutionStatusLostLease
		details += "; live workspace is not ready to restore: " + err.Error()
	} else if err := s.ConflictRestorer.ForcePushCommitWithLease(ctx, ws.Path, attempt.Branch, attempt.OriginalSHA, attempt.CandidateSHA); err != nil {
		status = storage.ConflictResolutionStatusLostLease
		details += "; restore lease failed: " + err.Error()
	} else if err := s.ConflictRestorer.Reset(ctx, ws.Path, attempt.OriginalSHA); err != nil {
		status = storage.ConflictResolutionStatusLostLease
		details += "; local workspace restore failed: " + err.Error()
	} else {
		details += fmt.Sprintf("; restored pull request branch to %s", attempt.OriginalSHA)
	}

	if updateErr := s.Store.UpdateConflictResolutionAttemptStatus(ctx, executionID, issueID, status, details, s.Now()); updateErr != nil {
		return true, "", fmt.Errorf("ci: update conflict resolution attempt for issue %s: %w", issueID, updateErr)
	}

	state, err = s.routeToNeedsInfo(ctx, executionID, issueID,
		"Forge's automatic merge-conflict repair candidate failed after publication.",
		details,
	)
	return true, state, err
}

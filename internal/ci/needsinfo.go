package ci

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/needsinfo"
	"github.com/Teagan42/forge/internal/storage"
)

// routeToNeedsInfo transitions an Issue already in CI_PENDING to
// NEEDS_INFO (issue 109: an unresolvable merge conflict, or review
// feedback too ambiguous for automated repair to act on safely). It mirrors
// engine.Engine.handleNeedsInfo's label/comment/checkpoint sequencing —
// AddLabel first (idempotent, safe to repeat), then a checkpoint saved
// before AddComment narrows the crash window, then the comment itself —
// so the same `forge resume` flow (engine.Resume) that resumes an
// IMPLEMENTING-triggered NEEDS_INFO also resumes one triggered here: both
// leave an identical storage.NeedsInfoCheckpoint behind.
func (s *Supervisor) routeToNeedsInfo(ctx context.Context, executionID, issueID, question, context string) (domain.IssueState, error) {
	label := s.Config.Blocked.Label
	labelEligible := label != "" && s.NeedsInfoTracker != nil
	if labelEligible {
		if err := s.NeedsInfoTracker.AddLabel(ctx, issueID, label); err != nil {
			return "", fmt.Errorf("ci: add needs-info label to issue %s: %w", issueID, err)
		}
	}

	checkpoint := storage.NeedsInfoCheckpoint{
		ExecutionID: executionID,
		IssueID:     issueID,
		Question:    question,
		Context:     context,
		LabelAdded:  labelEligible,
		CreatedAt:   s.Now(),
	}
	if err := s.Store.SaveNeedsInfoCheckpoint(ctx, checkpoint); err != nil {
		return "", fmt.Errorf("ci: save needs-info checkpoint for issue %s: %w", issueID, err)
	}

	if s.Config.Blocked.Comment && s.NeedsInfoTracker != nil {
		body := "Forge needs more information to continue:\n\n**Question:** " + question
		if context != "" {
			body += "\n\n**Context:** " + context
		}
		body = needsinfo.AppendCommentMarker(body, needsinfo.KindNeedsInfo, executionID, issueID)
		posted, err := s.NeedsInfoTracker.AddComment(ctx, issueID, body)
		if err != nil {
			return "", fmt.Errorf("ci: post needs-info comment on issue %s: %w", issueID, err)
		}
		checkpoint.CommentPosted = true
		checkpoint.CommentAuthor = posted.Author
		checkpoint.CommentPostedAt = posted.CreatedAt
		if err := s.Store.SaveNeedsInfoCheckpoint(ctx, checkpoint); err != nil {
			return "", fmt.Errorf("ci: save needs-info checkpoint for issue %s: %w", issueID, err)
		}
	}

	issue, err := s.Store.TransitionIssue(ctx, executionID, issueID, domain.StateNeedsInfo)
	if err != nil {
		return "", fmt.Errorf("ci: transition issue %s to NEEDS_INFO: %w", issueID, err)
	}
	return issue.State, nil
}

package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

// NeedsInfoTracker is the subset of tracker.Tracker the NEEDS_INFO handling
// needs: adding the configured blocked label and posting a structured
// comment. Depending on this narrow interface rather than tracker.Tracker
// keeps the handling backend-agnostic and its test double down to two
// methods (see IssueFetcher's doc comment for the same rationale).
type NeedsInfoTracker interface {
	AddLabel(ctx context.Context, id string, label string) error
	AddComment(ctx context.Context, id string, body string) error
}

// handleNeedsInfo implements the StatusNeedsInfo arm of Execute's result
// switch (see CONTEXT.md's needs-info resume flow, issue 07 and
// .scratch/forge-mvp/issues/28-needs-info-flow.md): it adds the configured
// blocked label, posts a structured comment with the Agent's question and
// context, persists a needs-info checkpoint, and transitions the Issue to
// NEEDS_INFO. It never removes the Workspace (the caller, Execute, simply
// does not call Workspaces.Cleanup on this path) and never creates a PR.
//
// Both the label and comment operations are idempotent: AddLabel is
// naturally idempotent per tracker.Tracker's contract, and the comment is
// guarded by the persisted checkpoint's CommentPosted flag so a repeated
// call for the same Execution/Issue (e.g. a crash-and-retry of this same
// step) does not double-post.
func (e *Engine) handleNeedsInfo(ctx context.Context, executionID, issueID, workerRef string, result agent.AgentResult) (domain.Issue, error) {
	if result.NeedsInfo == nil {
		return domain.Issue{}, fmt.Errorf("engine: agent reported NEEDS_INFO for issue %s with no NeedsInfo detail", issueID)
	}

	checkpoint, err := e.Store.GetNeedsInfoCheckpoint(ctx, executionID, issueID)
	if err != nil && !isNotFound(err) {
		return domain.Issue{}, fmt.Errorf("engine: load needs-info checkpoint for issue %s: %w", issueID, err)
	}
	alreadyCheckpointed := err == nil

	label := e.Config.Blocked.Label
	if label != "" && e.NeedsInfoTracker != nil {
		if err := e.NeedsInfoTracker.AddLabel(ctx, issueID, label); err != nil {
			return domain.Issue{}, fmt.Errorf("engine: add needs-info label to issue %s: %w", issueID, err)
		}
	}

	commentPosted := alreadyCheckpointed && checkpoint.CommentPosted
	if e.Config.Blocked.Comment && e.NeedsInfoTracker != nil && !commentPosted {
		body := needsInfoCommentBody(result.NeedsInfo, result.Summary)
		if err := e.NeedsInfoTracker.AddComment(ctx, issueID, body); err != nil {
			return domain.Issue{}, fmt.Errorf("engine: post needs-info comment on issue %s: %w", issueID, err)
		}
		commentPosted = true
	}

	if err := e.Store.SaveNeedsInfoCheckpoint(ctx, storage.NeedsInfoCheckpoint{
		ExecutionID:   executionID,
		IssueID:       issueID,
		Question:      result.NeedsInfo.Question,
		Reason:        result.NeedsInfo.Context,
		LabelAdded:    label != "",
		CommentPosted: commentPosted,
		CreatedAt:     e.Now(),
	}); err != nil {
		return domain.Issue{}, fmt.Errorf("engine: save needs-info checkpoint for issue %s: %w", issueID, err)
	}

	if err := e.appendEvent(ctx, executionID, issueID, "needsinfo.checkpoint_saved", map[string]string{
		"question": result.NeedsInfo.Question,
	}); err != nil {
		return domain.Issue{}, err
	}

	// Release the Worker slot. In today's single-issue Execute (ticket 18)
	// this is a no-op — there is no concurrency limiter to free a slot on —
	// but the Event models the transition so a future multi-issue Scheduler
	// (ticket 26) has an auditable point to hook a real release into.
	if err := e.appendEvent(ctx, executionID, issueID, "worker.released", map[string]string{
		"worker_ref": workerRef,
	}); err != nil {
		return domain.Issue{}, err
	}

	return e.transition(ctx, executionID, issueID, domain.StateNeedsInfo)
}

// needsInfoCommentBody renders the structured comment posted on NEEDS_INFO:
// the question the Agent needs answered, the supporting context explaining
// why it arose, and a brief summary of what was attempted.
func needsInfoCommentBody(detail *agent.NeedsInfoDetail, summary string) string {
	body := "Forge needs more information to continue:\n\n**Question:** " + detail.Question
	if detail.Context != "" {
		body += "\n\n**Reason:** " + detail.Context
	}
	if summary != "" {
		body += "\n\n**Context:** " + summary
	}
	return body
}

// isNotFound reports whether err wraps storage.ErrNotFound.
func isNotFound(err error) bool {
	return errors.Is(err, storage.ErrNotFound)
}

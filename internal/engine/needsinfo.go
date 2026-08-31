package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/review"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// NeedsInfoTracker is the subset of tracker.Tracker the NEEDS_INFO handling
// needs: adding the configured blocked label and posting a structured
// comment. Depending on this narrow interface rather than tracker.Tracker
// keeps the handling backend-agnostic and its test double down to two
// methods (see IssueFetcher's doc comment for the same rationale).
type NeedsInfoTracker interface {
	AddLabel(ctx context.Context, id string, label string) error

	// AddComment posts a comment and returns it normalized, including the
	// tracker-server-clock identity/timestamp — see
	// storage.NeedsInfoCheckpoint's CommentAuthor/CommentPostedAt doc
	// comment for why handleNeedsInfo needs the tracker's own values rather
	// than a locally captured author/clock.
	AddComment(ctx context.Context, id string, body string) (tracker.Comment, error)
}

// handleNeedsInfo implements the StatusNeedsInfo arm of Execute's result
// switch (see CONTEXT.md's needs-info resume flow, issue 07 and
// .scratch/forge-mvp/issues/28-needs-info-flow.md): it adds the configured
// blocked label, posts a structured comment with the Agent's question and
// context, persists a needs-info checkpoint, and transitions the Issue to
// NEEDS_INFO. It never removes the Workspace (the caller, Execute, simply
// does not call Workspaces.Cleanup on this path) and never creates a PR.
//
// AddLabel is naturally idempotent per tracker.Tracker's contract and is
// called on every invocation. The comment is guarded by the persisted
// checkpoint's CommentPosted flag, and — to narrow (not eliminate) the
// crash window between AddComment succeeding and that flag being
// persisted — an "intent" checkpoint (Question/Context/LabelAdded, not yet
// CommentPosted) is saved before AddComment is called, not after. This
// means a retry that lands after the intent checkpoint but before the
// post-comment checkpoint update will still see CommentPosted=false and
// will still re-post: the two operations (an external HTTP POST and a
// local DB write) cannot be made atomic without a tracker-side idempotency
// key, which GitHub's comment API does not offer. Full double-post
// prevention across a crash mid-post is out of scope for this ticket.
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
	labelEligible := label != "" && e.NeedsInfoTracker != nil
	if labelEligible {
		if err := e.NeedsInfoTracker.AddLabel(ctx, issueID, label); err != nil {
			return domain.Issue{}, fmt.Errorf("engine: add needs-info label to issue %s: %w", issueID, err)
		}
	}

	if !alreadyCheckpointed {
		checkpoint = storage.NeedsInfoCheckpoint{
			ExecutionID: executionID,
			IssueID:     issueID,
			Question:    result.NeedsInfo.Question,
			Context:     result.NeedsInfo.Context,
			LabelAdded:  labelEligible,
			CreatedAt:   e.Now(),
		}
		if err := e.Store.SaveNeedsInfoCheckpoint(ctx, checkpoint); err != nil {
			return domain.Issue{}, fmt.Errorf("engine: save needs-info checkpoint for issue %s: %w", issueID, err)
		}
	}

	if e.Config.Blocked.Comment && e.NeedsInfoTracker != nil && !checkpoint.CommentPosted {
		body := needsInfoCommentBody(result.NeedsInfo, result.Summary)
		posted, err := e.NeedsInfoTracker.AddComment(ctx, issueID, body)
		if err != nil {
			return domain.Issue{}, fmt.Errorf("engine: post needs-info comment on issue %s: %w", issueID, err)
		}
		checkpoint.CommentPosted = true
		checkpoint.CommentAuthor = posted.Author
		checkpoint.CommentPostedAt = posted.CreatedAt
		if err := e.Store.SaveNeedsInfoCheckpoint(ctx, checkpoint); err != nil {
			return domain.Issue{}, fmt.Errorf("engine: save needs-info checkpoint for issue %s: %w", issueID, err)
		}
	}

	if err := e.appendEvent(ctx, executionID, issueID, "needsinfo.checkpoint_saved", map[string]string{
		"question": result.NeedsInfo.Question,
	}); err != nil {
		return domain.Issue{}, err
	}

	// Release the Worker slot. In today's single-issue Execute (ticket 18)
	// this is a no-op — there is no concurrency limiter to free a slot on —
	// but the Event models the transition so a future multi-issue Scheduler
	// (ticket 26) has an auditable point to hook a real release into. This
	// representation (an informational Event) may change once 26 makes
	// slot release real.
	if err := e.appendEvent(ctx, executionID, issueID, "worker.released", map[string]string{
		"worker_ref": workerRef,
	}); err != nil {
		return domain.Issue{}, err
	}
	if err := e.Store.ReleaseWorkerClaim(ctx, executionID, issueID); err != nil {
		return domain.Issue{}, fmt.Errorf("engine: release worker claim for issue %s: %w", issueID, err)
	}

	return e.transition(ctx, executionID, issueID, domain.StateNeedsInfo)
}

// needsInfoCommentBody renders the structured comment posted on NEEDS_INFO.
// Each header names exactly the AgentResult field it renders, so the same
// value is never called by different names in different places (the
// comment, the checkpoint, and the Agent's own field names all agree):
// Question <- detail.Question, Context <- detail.Context, Summary <-
// result.Summary.
func needsInfoCommentBody(detail *agent.NeedsInfoDetail, summary string) string {
	body := "Forge needs more information to continue:\n\n**Question:** " + detail.Question
	if detail.Context != "" {
		body += "\n\n**Context:** " + detail.Context
	}
	if summary != "" {
		body += "\n\n**Summary:** " + summary
	}
	return body
}

// isNotFound reports whether err wraps storage.ErrNotFound.
func isNotFound(err error) bool {
	return errors.Is(err, storage.ErrNotFound)
}

// escalateReviewToNeedsInfo routes a review outcome Forge's autonomous loop
// cannot safely resolve on its own to a human, via the same NEEDS_INFO
// mechanism (handleNeedsInfo) used elsewhere, rather than the FAILED
// terminal or a false APPROVED (issue #161's governing principle: a false
// APPROVED is the worst outcome). It covers two distinct call sites:
//
//   - runReview, when the Reviewer itself returns review.VerdictInconclusive
//     (it could not certify full axis coverage);
//   - runRepairLoop's review branch, when a standing
//     review.VerdictChangesRequired verdict finds RetryBudget.Review already
//     exhausted.
//
// Neither case consumes or even inspects any RetryBudget counter: this is
// Forge's own escalation, not another repair attempt the Agent gets a turn
// at, so the review-rejection counter (ADR-0007) is left untouched. question
// and reviewContext become the synthetic AgentResult's NeedsInfo.Question/
// Context, rendered into the same structured comment (needsInfoCommentBody)
// a real Agent-reported NEEDS_INFO would produce.
func (e *Engine) escalateReviewToNeedsInfo(ctx context.Context, executionID, issueID, question, reviewContext string) (domain.Issue, error) {
	synthetic := agent.AgentResult{
		Status:  agent.StatusNeedsInfo,
		Summary: question,
		NeedsInfo: &agent.NeedsInfoDetail{
			Question: question,
			Context:  reviewContext,
		},
	}
	return e.handleNeedsInfo(ctx, executionID, issueID, workerRef(executionID, issueID), synthetic)
}

// reviewFindingsContext renders findings as short bullet lines for a
// NEEDS_INFO comment's Context field, so a human reading the escalation
// sees exactly what the surviving/standing review findings were without
// needing to open the stored ReviewRun separately.
func reviewFindingsContext(findings []review.Finding) string {
	if len(findings) == 0 {
		return "no findings were attached to this review outcome."
	}
	lines := make([]string, 0, len(findings))
	for _, f := range findings {
		line := fmt.Sprintf("- [%s/%s] %s", f.Severity, f.Axis, f.Message)
		if f.File != "" {
			line = fmt.Sprintf("%s (%s:%d)", line, f.File, f.Line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

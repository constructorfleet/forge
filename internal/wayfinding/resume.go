package wayfinding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/needsinfo"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// LocalAnswerAuthor is the author recorded on a Decision answer supplied
// through the local, tracker-less answer channel (AnswerDecisionLocally). It
// marks the comment as operator input rather than a tracker user.
const LocalAnswerAuthor = "local"

// ResumeDecisionTracker is the subset of tracker.Tracker ResumeDecision needs:
// re-fetching a Feature's comments to detect new human input since the
// NEEDS_HUMAN checkpoint.
type ResumeDecisionTracker interface {
	GetComments(ctx context.Context, id string) ([]tracker.Comment, error)
}

// ResumeDecisionStore is the subset of storage.Store ResumeDecision needs.
type ResumeDecisionStore interface {
	GetDecisionCheckpoint(ctx context.Context, executionID, decisionID string) (storage.DecisionCheckpoint, error)
	SaveDecisionCheckpointWithEvent(ctx context.Context, checkpoint storage.DecisionCheckpoint, event storage.Event) error
	UpdatePlanningStatus(ctx context.Context, executionID string, status domain.PlanningStatus) error
	LoadPlanningExecution(ctx context.Context, executionID string) (domain.PlanningExecution, error)
}

// ResumedDecisionContext is the focused context the next wayfinding Loop
// invocation for a resumed Decision should receive: the original Decision
// artifact, the question that was asked, and only the comments posted after
// the needs-human checkpoint.
type ResumedDecisionContext struct {
	Decision         *planning.Artifact
	PreviousQuestion string
	NewComments      []tracker.Comment
}

// ResumeDecisionResult is the outcome of ResumeDecision.
type ResumeDecisionResult struct {
	// Resumed is true only when new human input was detected and the
	// Planning Execution transitioned NEEDS_HUMAN -> ACTIVE.
	Resumed bool

	// Context is the focused resumed context, populated whenever a
	// checkpoint exists (even if Resumed is false, so a caller can inspect
	// what is still missing).
	Context ResumedDecisionContext
}

// ResumeDecision implements the resume half of wayfinding's NEEDS_HUMAN
// checkpoint (ticket 15b). It re-fetches the Feature's comments via trk,
// detects comments posted after forge's own needs-human checkpoint comment,
// and — only if at least one new *human* comment exists — transitions the
// Planning Execution's runtime status from NEEDS_HUMAN to ACTIVE and
// persists the resumed, focused context for the next Loop invocation.
//
// "New" is judged against checkpoint.CommentPostedAt — the tracker-server
// clock timestamp of forge's own posted comment, as returned by
// tracker.Tracker.AddComment — rather than checkpoint.CreatedAt (a local
// clock). Both guard against forge's own needs-human comment being
// misread as new human input under local/tracker clock skew. Any comment
// carrying Forge's own needsinfo.KindNeedsHuman marker is excluded
// outright, not by comment author: excluding by author would silently
// drop a genuine human answer posted through the same account Forge
// itself posts as (a shared bot account, an automation, or the TUI), so
// the marker identifies Forge's own comment by content instead. If no
// comment was ever posted (e.g. PauseHandler.PostComment configured
// false), checkpoint.CommentPostedAt is zero and ResumeDecision falls
// back to checkpoint.CreatedAt — a real, if lesser, skew exposure in that
// configuration, since there is no tracker-clock anchor to compare against.
//
// The ACTIVE transition is performed last, after the checkpoint's resumed
// context and the "decision.resumed" Event are durably saved together (one
// transaction, via SaveDecisionCheckpointWithEvent): if that save fails, the
// Planning Execution remains in NEEDS_HUMAN and `forge resume` can simply be
// re-run.
func ResumeDecision(ctx context.Context, store ResumeDecisionStore, trk ResumeDecisionTracker, executionID, decisionID string, now func() time.Time) (ResumeDecisionResult, error) {
	checkpoint, err := store.GetDecisionCheckpoint(ctx, executionID, decisionID)
	if err != nil {
		return ResumeDecisionResult{}, fmt.Errorf("wayfinding: resume decision %s: %w", decisionID, err)
	}

	exec, err := store.LoadPlanningExecution(ctx, executionID)
	if err != nil {
		return ResumeDecisionResult{}, fmt.Errorf("wayfinding: resume decision %s: load execution: %w", decisionID, err)
	}

	decision, err := loadDecisionFromCheckpoint(ctx, store, executionID, decisionID, checkpoint)
	if err != nil {
		return ResumeDecisionResult{}, fmt.Errorf("wayfinding: resume decision %s: load decision: %w", decisionID, err)
	}

	comments, err := trk.GetComments(ctx, exec.FeatureID)
	if err != nil {
		if !errors.Is(err, tracker.ErrInvalidIssueID) {
			return ResumeDecisionResult{}, fmt.Errorf("wayfinding: resume decision %s: fetch comments: %w", decisionID, err)
		}
		// The Feature id is not a tracker issue (a local Feature slug). The
		// answer arrives through the local answer channel
		// (AnswerDecisionLocally), which resumes the decision directly, so a
		// tracker-comment poll is a harmless no-op here.
		comments = nil
	}

	baseline := checkpoint.CommentPostedAt
	if baseline.IsZero() {
		baseline = checkpoint.CreatedAt
	}

	var newComments []tracker.Comment
	for _, c := range comments {
		if needsinfo.IsForgeComment(c.Body, needsinfo.KindNeedsHuman, executionID, decisionID) {
			continue // forge's own posted comment, never "new human input"
		}
		if c.CreatedAt.After(baseline) {
			newComments = append(newComments, c)
		}
	}

	resumedCtx := ResumedDecisionContext{
		Decision:         decision,
		PreviousQuestion: checkpoint.Question,
		NewComments:      newComments,
	}

	if len(newComments) == 0 {
		return ResumeDecisionResult{Resumed: false, Context: resumedCtx}, nil
	}

	if err := commitResumedDecision(ctx, store, &checkpoint, resumedCtx, executionID, decisionID, now()); err != nil {
		return ResumeDecisionResult{}, err
	}

	return ResumeDecisionResult{Resumed: true, Context: resumedCtx}, nil
}

// AnswerDecisionLocally records a human answer to a paused Decision without a
// tracker. It writes the answer into the Decision's checkpoint as the resumed
// context and transitions the Planning Execution from NEEDS_HUMAN to ACTIVE,
// exactly as ResumeDecision does for a tracker comment. It is the answer
// channel for a local Feature slug that has no backing tracker issue (see
// PauseHandler's localFeature path). The wayfinding Loop then re-resolves the
// Decision from the answer, since gatherResumedAnswers reads the same resumed
// context ResumeDecision writes.
func AnswerDecisionLocally(ctx context.Context, store ResumeDecisionStore, executionID, decisionID, answer string, now func() time.Time) (ResumeDecisionResult, error) {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return ResumeDecisionResult{}, fmt.Errorf("wayfinding: answer decision %s: empty answer", decisionID)
	}

	checkpoint, err := store.GetDecisionCheckpoint(ctx, executionID, decisionID)
	if err != nil {
		return ResumeDecisionResult{}, fmt.Errorf("wayfinding: answer decision %s: %w", decisionID, err)
	}
	if checkpoint.ResumedAt != nil {
		// Already answered; report the recorded resumed context rather than
		// double-record. The driver treats this as no new progress.
		return ResumeDecisionResult{Resumed: false}, nil
	}

	decision, err := loadDecisionFromCheckpoint(ctx, store, executionID, decisionID, checkpoint)
	if err != nil {
		return ResumeDecisionResult{}, fmt.Errorf("wayfinding: answer decision %s: load decision: %w", decisionID, err)
	}

	answeredAt := now()
	resumedCtx := ResumedDecisionContext{
		Decision:         decision,
		PreviousQuestion: checkpoint.Question,
		NewComments:      []tracker.Comment{{Author: LocalAnswerAuthor, Body: trimmed, CreatedAt: answeredAt}},
	}

	if err := commitResumedDecision(ctx, store, &checkpoint, resumedCtx, executionID, decisionID, answeredAt); err != nil {
		return ResumeDecisionResult{}, err
	}

	return ResumeDecisionResult{Resumed: true, Context: resumedCtx}, nil
}

// commitResumedDecision persists a Decision's resumed context and the
// "decision.resumed" Event together, then transitions the Planning Execution
// to ACTIVE. It is the shared tail of ResumeDecision (answer from a tracker
// comment) and AnswerDecisionLocally (answer from the local channel): the
// ACTIVE transition runs last, so a failed save leaves the execution in
// NEEDS_HUMAN and the answer can be re-recorded.
func commitResumedDecision(ctx context.Context, store ResumeDecisionStore, checkpoint *storage.DecisionCheckpoint, resumedCtx ResumedDecisionContext, executionID, decisionID string, resumedAt time.Time) error {
	contextJSON, err := json.Marshal(resumedCtx)
	if err != nil {
		return fmt.Errorf("wayfinding: resume decision %s: marshal resumed context: %w", decisionID, err)
	}

	checkpoint.ResumedAt = &resumedAt
	checkpoint.ResumedContext = string(contextJSON)
	event, err := storage.MarshalEvent(executionID, "decision.resumed", resumedAt, struct {
		DecisionID string `json:"decision_id"`
	}{DecisionID: decisionID})
	if err != nil {
		return fmt.Errorf("wayfinding: resume decision %s: build resumed event: %w", decisionID, err)
	}
	if err := store.SaveDecisionCheckpointWithEvent(ctx, *checkpoint, event); err != nil {
		return fmt.Errorf("wayfinding: resume decision %s: save checkpoint: %w", decisionID, err)
	}

	if err := store.UpdatePlanningStatus(ctx, executionID, domain.PlanningStatusActive); err != nil {
		return fmt.Errorf("wayfinding: resume decision %s: update planning status: %w", decisionID, err)
	}

	return nil
}

// loadDecisionFromCheckpoint reloads the Decision artifact from the checkpoint's
// DecisionRevision. In a real implementation this would load from the planning
// artifacts on disk. For now we reconstruct a minimal artifact with the
// checkpoint's question.
func loadDecisionFromCheckpoint(ctx context.Context, store ResumeDecisionStore, executionID, decisionID string, checkpoint storage.DecisionCheckpoint) (*planning.Artifact, error) {
	d := &planning.Artifact{
		Kind:     planning.KindDecision,
		State:    decisiongraph.StateNeedsHuman,
		Sections: []planning.Section{{Heading: "Question", Body: checkpoint.Question}},
	}
	d.Revision = checkpoint.DecisionRevision
	return d, nil
}

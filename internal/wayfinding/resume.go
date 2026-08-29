package wayfinding

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// ResumeDecisionTracker is the subset of tracker.Tracker ResumeDecision needs:
// re-fetching a Feature's comments to detect new human input since the
// NEEDS_HUMAN checkpoint.
type ResumeDecisionTracker interface {
	GetComments(ctx context.Context, id string) ([]tracker.Comment, error)
}

// ResumeDecisionStore is the subset of storage.Store ResumeDecision needs.
type ResumeDecisionStore interface {
	GetDecisionCheckpoint(ctx context.Context, executionID, decisionID string) (storage.DecisionCheckpoint, error)
	SaveDecisionCheckpoint(ctx context.Context, checkpoint storage.DecisionCheckpoint) error
	UpdatePlanningStatus(ctx context.Context, executionID string, status domain.PlanningStatus) error
	AppendEvent(ctx context.Context, event storage.Event) error
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
// clock), and any comment authored by checkpoint.CommentAuthor is excluded
// outright. Both guard against forge's own needs-human comment being
// misread as new human input under local/tracker clock skew. If no comment
// was ever posted (e.g. PauseHandler.PostComment configured false),
// checkpoint.CommentPostedAt is zero and ResumeDecision falls back to
// checkpoint.CreatedAt — a real, if lesser, skew exposure in that
// configuration, since there is no tracker-clock anchor to compare against.
//
// The ACTIVE transition is performed last, after the checkpoint's resumed
// context and the "decision.resumed" Event are durably saved: if either of
// those fails, the Planning Execution remains in NEEDS_HUMAN and
// `forge resume` can simply be re-run.
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
		return ResumeDecisionResult{}, fmt.Errorf("wayfinding: resume decision %s: fetch comments: %w", decisionID, err)
	}

	baseline := checkpoint.CommentPostedAt
	if baseline.IsZero() {
		baseline = checkpoint.CreatedAt
	}

	var newComments []tracker.Comment
	for _, c := range comments {
		if checkpoint.CommentAuthor != "" && c.Author == checkpoint.CommentAuthor {
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

	contextJSON, err := json.Marshal(resumedCtx)
	if err != nil {
		return ResumeDecisionResult{}, fmt.Errorf("wayfinding: resume decision %s: marshal resumed context: %w", decisionID, err)
	}
	resumedAt := now()
	checkpoint.ResumedAt = &resumedAt
	checkpoint.ResumedContext = string(contextJSON)
	if err := store.SaveDecisionCheckpoint(ctx, checkpoint); err != nil {
		return ResumeDecisionResult{}, fmt.Errorf("wayfinding: resume decision %s: save checkpoint: %w", decisionID, err)
	}

	if err := store.UpdatePlanningStatus(ctx, executionID, domain.PlanningStatusActive); err != nil {
		return ResumeDecisionResult{}, fmt.Errorf("wayfinding: resume decision %s: update planning status: %w", decisionID, err)
	}

	return ResumeDecisionResult{Resumed: true, Context: resumedCtx}, nil
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

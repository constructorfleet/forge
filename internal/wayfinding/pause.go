package wayfinding

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/decisionresolution"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/needsinfo"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// NeedsHumanHandler is called by Loop when a Decision's resolution reports
// NEEDS_HUMAN instead of a normal resolution: it performs whatever
// checkpoint/tracker/runtime-status side effects the caller needs (see
// PauseHandler.Handle for Forge's own implementation) and returns the
// Decision Artifact, paused, for Loop to persist and drop off the frontier
// (decisiongraph.Pause, decisiongraph.Frontier). decision is the Decision's
// current (still-unresolved) Artifact.
type NeedsHumanHandler func(ctx context.Context, decisionID string, decision *planning.Artifact, detail decisionresolution.NeedsHumanDetail) (*planning.Artifact, error)

// NeedsHumanTracker is the subset of tracker.Tracker PauseHandler needs:
// adding the configured blocked label and posting a structured comment on
// the Feature's tracker issue. Mirrors internal/engine.NeedsInfoTracker's
// shape and rationale exactly, scoped to a Feature (identified by
// PauseHandler.FeatureID) rather than an Issue.
type NeedsHumanTracker interface {
	AddLabel(ctx context.Context, id string, label string) error
	AddComment(ctx context.Context, id string, body string) (tracker.Comment, error)
}

// CheckpointStore is the subset of storage.Store PauseHandler needs: saving
// and reloading a Decision's NEEDS_HUMAN checkpoint, and reflecting the
// pause onto the Planning Execution's runtime status.
type CheckpointStore interface {
	GetDecisionCheckpoint(ctx context.Context, executionID, decisionID string) (storage.DecisionCheckpoint, error)
	SaveDecisionCheckpoint(ctx context.Context, checkpoint storage.DecisionCheckpoint) error
	UpdatePlanningStatus(ctx context.Context, executionID string, status domain.PlanningStatus) error
}

// PauseHandler implements NeedsHumanHandler: Forge's own NEEDS_HUMAN
// checkpoint-and-pause mechanics (ticket 15a), reusing Phase 1's
// needs-info comment/label pattern (internal/engine.handleNeedsInfo)
// against the Feature's tracker issue (FeatureID) rather than a coding
// Issue.
type PauseHandler struct {
	// ExecutionID identifies the Planning Execution this handler pauses --
	// the checkpoint key and the runtime-status update both scope to it.
	ExecutionID string

	// FeatureID is the tracker issue Forge posts the needs-human label and
	// comment to, mirroring how Phase 1's NeedsInfoTracker addresses a
	// coding Issue by ID.
	FeatureID string

	Store   CheckpointStore
	Tracker NeedsHumanTracker

	// Label is the tracker label added on pause. Blank disables labeling.
	Label string
	// PostComment gates posting the needs-human comment. False disables
	// commenting even when Tracker is set.
	PostComment bool

	// Now is a seam for deterministic tests; a nil Now defaults to
	// time.Now().UTC().
	Now func() time.Time
}

// Handle records decisionID's checkpoint (idempotently -- a checkpoint
// already on file is reused rather than re-created), adds the configured
// label, posts the needs-human comment at most once (guarded by the
// checkpoint's CommentPosted flag, the same crash-window-narrowing
// intent-then-post sequencing handleNeedsInfo uses), sets the Planning
// Execution's runtime status to NEEDS_HUMAN, and returns decision paused
// (decisiongraph.Pause) for Loop to persist.
func (p *PauseHandler) Handle(ctx context.Context, decisionID string, decision *planning.Artifact, detail decisionresolution.NeedsHumanDetail) (*planning.Artifact, error) {
	checkpoint, err := p.Store.GetDecisionCheckpoint(ctx, p.ExecutionID, decisionID)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return nil, fmt.Errorf("wayfinding: load decision checkpoint for %s: %w", decisionID, err)
	}
	alreadyCheckpointed := err == nil

	labelEligible := p.Label != "" && p.Tracker != nil
	if labelEligible {
		if err := p.Tracker.AddLabel(ctx, p.FeatureID, p.Label); err != nil {
			return nil, fmt.Errorf("wayfinding: add needs-human label for decision %s: %w", decisionID, err)
		}
	}

	if !alreadyCheckpointed {
		checkpoint = storage.DecisionCheckpoint{
			ExecutionID:      p.ExecutionID,
			DecisionID:       decisionID,
			DecisionRevision: planning.ComputeRevision(decision),
			Question:         detail.Question,
			Context:          detail.Context,
			LabelAdded:       labelEligible,
			CreatedAt:        p.now(),
		}
		if err := p.Store.SaveDecisionCheckpoint(ctx, checkpoint); err != nil {
			return nil, fmt.Errorf("wayfinding: save decision checkpoint for %s: %w", decisionID, err)
		}
	}

	if p.PostComment && p.Tracker != nil && !checkpoint.CommentPosted {
		posted, err := p.Tracker.AddComment(ctx, p.FeatureID, needsHumanCommentBody(p.ExecutionID, decisionID, detail))
		if err != nil {
			return nil, fmt.Errorf("wayfinding: post needs-human comment for decision %s: %w", decisionID, err)
		}
		checkpoint.CommentPosted = true
		checkpoint.CommentAuthor = posted.Author
		checkpoint.CommentPostedAt = posted.CreatedAt
		if err := p.Store.SaveDecisionCheckpoint(ctx, checkpoint); err != nil {
			return nil, fmt.Errorf("wayfinding: save decision checkpoint for %s: %w", decisionID, err)
		}
	}

	if err := p.Store.UpdatePlanningStatus(ctx, p.ExecutionID, domain.PlanningStatusNeedsHuman); err != nil {
		return nil, fmt.Errorf("wayfinding: set planning execution %s to NEEDS_HUMAN: %w", p.ExecutionID, err)
	}

	return decisiongraph.Pause(decision), nil
}

func (p *PauseHandler) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now().UTC()
}

// needsHumanCommentBody renders the structured comment PauseHandler posts on
// NEEDS_HUMAN, mirroring internal/engine's needsInfoCommentBody shape. It
// carries Forge's hidden needsinfo.KindNeedsHuman marker so ResumeDecision
// can recognize Forge's own posted comment by content, not by
// tracker-account identity (see internal/needsinfo.CommentMarker for the
// execution-phase equivalent this mirrors). A comment_author match is not a
// safe proxy: any tool that answers a paused Decision using the same
// GITHUB_TOKEN Forge itself posts as -- a shared bot account, an
// automation, or the TUI -- would otherwise be silently excluded as
// "forge's own comment".
func needsHumanCommentBody(executionID, decisionID string, detail decisionresolution.NeedsHumanDetail) string {
	body := fmt.Sprintf("Forge's wayfinding needs your input on decision `%s`:\n\n**Question:** %s", decisionID, detail.Question)
	if detail.Context != "" {
		body += "\n\n**Context:** " + detail.Context
	}
	return needsinfo.AppendCommentMarker(body, needsinfo.KindNeedsHuman, executionID, decisionID)
}

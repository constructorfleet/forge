package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// ResumeTracker is the subset of tracker.Tracker `forge resume` needs:
// re-fetching an Issue's comments to detect new human input since the
// needs-info checkpoint.
type ResumeTracker interface {
	GetComments(ctx context.Context, id string) ([]tracker.Comment, error)
}

// ResumeStore is the subset of storage.Store `forge resume` needs. A
// narrower interface than storage.Store, for the same reason StatusStore
// is narrower (see status.go).
type ResumeStore interface {
	GetIssue(ctx context.Context, executionID, issueID string) (domain.Issue, error)
	GetNeedsInfoCheckpoint(ctx context.Context, executionID, issueID string) (storage.NeedsInfoCheckpoint, error)
	SaveNeedsInfoCheckpoint(ctx context.Context, checkpoint storage.NeedsInfoCheckpoint) error
	TransitionIssue(ctx context.Context, executionID, issueID string, to domain.IssueState) (domain.Issue, error)
	AppendEvent(ctx context.Context, event storage.Event) error
}

// ResumedContext is the focused context the next Worker invocation for a
// resumed Issue should receive: the original Issue, the question that was
// asked, and only the comments posted after the needs-info checkpoint —
// never the full comment history (see CONTEXT.md's needs-info resume flow,
// issue 07: "original issue context + previous question + ONLY the new
// comments").
type ResumedContext struct {
	Issue            domain.Issue
	PreviousQuestion string
	NewComments      []tracker.Comment
}

// ResumeResult is the outcome of Resume.
type ResumeResult struct {
	// Issue is the Issue's state after Resume returns: READY if new human
	// input was found, unchanged (still NEEDS_INFO) otherwise.
	Issue domain.Issue

	// Resumed is true only when new human input was detected and the Issue
	// transitioned NEEDS_INFO -> READY.
	Resumed bool

	// Context is the focused resumed context, populated whenever a
	// checkpoint exists (even if Resumed is false, so a caller can inspect
	// what is still missing).
	Context ResumedContext
}

// Resume implements `forge resume <execution-id>` (see
// .scratch/forge-mvp/issues/28-needs-info-flow.md): it re-fetches issueID's
// comments via trk, detects comments posted after the persisted needs-info
// checkpoint's timestamp, and — only if at least one new comment exists —
// transitions the Issue NEEDS_INFO -> READY (the sole legal edge out of
// NEEDS_INFO; see domain.state.go) and persists the resumed, focused
// context for the next Worker invocation. Resume errors if no checkpoint
// was ever recorded for issueID (Store.GetNeedsInfoCheckpoint returns
// storage.ErrNotFound) — there is nothing to resume.
func Resume(ctx context.Context, store ResumeStore, trk ResumeTracker, executionID, issueID string, now func() time.Time) (ResumeResult, error) {
	checkpoint, err := store.GetNeedsInfoCheckpoint(ctx, executionID, issueID)
	if err != nil {
		return ResumeResult{}, fmt.Errorf("engine: resume issue %s: %w", issueID, err)
	}

	issue, err := store.GetIssue(ctx, executionID, issueID)
	if err != nil {
		return ResumeResult{}, fmt.Errorf("engine: resume issue %s: %w", issueID, err)
	}

	comments, err := trk.GetComments(ctx, issueID)
	if err != nil {
		return ResumeResult{}, fmt.Errorf("engine: resume issue %s: fetch comments: %w", issueID, err)
	}

	var newComments []tracker.Comment
	for _, c := range comments {
		if c.CreatedAt.After(checkpoint.CreatedAt) {
			newComments = append(newComments, c)
		}
	}

	resumedCtx := ResumedContext{
		Issue:            issue,
		PreviousQuestion: checkpoint.Question,
		NewComments:      newComments,
	}

	if len(newComments) == 0 {
		return ResumeResult{Issue: issue, Resumed: false, Context: resumedCtx}, nil
	}

	issue, err = store.TransitionIssue(ctx, executionID, issueID, domain.StateReady)
	if err != nil {
		return ResumeResult{}, fmt.Errorf("engine: resume issue %s: %w", issueID, err)
	}

	contextJSON, err := json.Marshal(resumedCtx)
	if err != nil {
		return ResumeResult{}, fmt.Errorf("engine: resume issue %s: marshal resumed context: %w", issueID, err)
	}
	resumedAt := now()
	checkpoint.ResumedAt = &resumedAt
	checkpoint.ResumedContext = string(contextJSON)
	if err := store.SaveNeedsInfoCheckpoint(ctx, checkpoint); err != nil {
		return ResumeResult{}, fmt.Errorf("engine: resume issue %s: save checkpoint: %w", issueID, err)
	}

	eventData, err := json.Marshal(map[string]int{"new_comments": len(newComments)})
	if err != nil {
		return ResumeResult{}, fmt.Errorf("engine: resume issue %s: marshal event: %w", issueID, err)
	}
	if err := store.AppendEvent(ctx, storage.Event{
		ExecutionID: executionID,
		IssueID:     issueID,
		Type:        "needsinfo.resumed",
		Data:        string(eventData),
		OccurredAt:  resumedAt,
	}); err != nil {
		return ResumeResult{}, fmt.Errorf("engine: resume issue %s: append event: %w", issueID, err)
	}

	return ResumeResult{Issue: issue, Resumed: true, Context: resumedCtx}, nil
}

package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

func seedIssue(t *testing.T, store *storage.SQLiteStore, executionID, issueID string) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{ID: executionID, BaseRevision: "abc123", StartedAt: time.Now()}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	issue := domain.Issue{
		ID: issueID, ExecutionID: executionID,
		State: domain.StatePending, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
}

func TestNeedsInfoCheckpoint_SaveAndGet_RoundTrips(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssue(t, store, "exec-1", "7")

	createdAt := time.Now().UTC().Truncate(time.Second)
	commentPostedAt := createdAt.Add(time.Second)
	checkpoint := storage.NeedsInfoCheckpoint{
		ExecutionID:     "exec-1",
		IssueID:         "7",
		Question:        "which config flag?",
		Context:         "ambiguous flags",
		LabelAdded:      true,
		CommentPosted:   true,
		CommentAuthor:   "forge-bot",
		CommentPostedAt: commentPostedAt,
		CreatedAt:       createdAt,
	}
	if err := store.SaveNeedsInfoCheckpoint(ctx, checkpoint); err != nil {
		t.Fatalf("SaveNeedsInfoCheckpoint: %v", err)
	}

	got, err := store.GetNeedsInfoCheckpoint(ctx, "exec-1", "7")
	if err != nil {
		t.Fatalf("GetNeedsInfoCheckpoint: %v", err)
	}
	if got.Question != checkpoint.Question || got.Context != checkpoint.Context {
		t.Errorf("got = %+v, want question/context to match", got)
	}
	if !got.LabelAdded || !got.CommentPosted {
		t.Errorf("got.LabelAdded=%v CommentPosted=%v, want both true", got.LabelAdded, got.CommentPosted)
	}
	if got.CommentAuthor != "forge-bot" {
		t.Errorf("got.CommentAuthor = %q, want forge-bot", got.CommentAuthor)
	}
	if !got.CommentPostedAt.Equal(commentPostedAt) {
		t.Errorf("got.CommentPostedAt = %v, want %v", got.CommentPostedAt, commentPostedAt)
	}
	if !got.CreatedAt.Equal(createdAt) {
		t.Errorf("got.CreatedAt = %v, want %v", got.CreatedAt, createdAt)
	}
	if got.ResumedAt != nil {
		t.Errorf("got.ResumedAt = %v, want nil before resume", got.ResumedAt)
	}
}

func TestNeedsInfoCheckpoint_SaveTwice_Upserts(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssue(t, store, "exec-2", "9")

	first := storage.NeedsInfoCheckpoint{
		ExecutionID: "exec-2", IssueID: "9",
		Question: "q1", CreatedAt: time.Now().UTC(),
	}
	if err := store.SaveNeedsInfoCheckpoint(ctx, first); err != nil {
		t.Fatalf("SaveNeedsInfoCheckpoint (first): %v", err)
	}

	resumedAt := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	second := first
	second.CommentPosted = true
	second.ResumedAt = &resumedAt
	second.ResumedContext = `{"issue":{}}`
	if err := store.SaveNeedsInfoCheckpoint(ctx, second); err != nil {
		t.Fatalf("SaveNeedsInfoCheckpoint (second): %v", err)
	}

	got, err := store.GetNeedsInfoCheckpoint(ctx, "exec-2", "9")
	if err != nil {
		t.Fatalf("GetNeedsInfoCheckpoint: %v", err)
	}
	if !got.CommentPosted {
		t.Error("got.CommentPosted = false, want true after upsert")
	}
	if got.ResumedAt == nil || !got.ResumedAt.Equal(resumedAt) {
		t.Errorf("got.ResumedAt = %v, want %v", got.ResumedAt, resumedAt)
	}
	if got.ResumedContext != second.ResumedContext {
		t.Errorf("got.ResumedContext = %q, want %q", got.ResumedContext, second.ResumedContext)
	}
}

func TestNeedsInfoCheckpoint_GetMissing_ReturnsNotFound(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	if _, err := store.GetNeedsInfoCheckpoint(ctx, "exec-none", "none"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("GetNeedsInfoCheckpoint: err = %v, want ErrNotFound", err)
	}
}

package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

func TestRecordCIRun_PersistsAndAppendsEvent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-ci", "issue-1", domain.StateCIPending)

	run := storage.CIRun{
		ExecutionID: "exec-ci",
		IssueID:     "issue-1",
		Status:      storage.CIRunStatusFailed,
		CheckName:   "build",
		Details:     "boom",
		CheckedAt:   time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
	}
	if err := store.RecordCIRun(ctx, run); err != nil {
		t.Fatalf("RecordCIRun: %v", err)
	}

	runs, err := store.CIRunsByIssue(ctx, "exec-ci", "issue-1")
	if err != nil {
		t.Fatalf("CIRunsByIssue: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d CI runs, want 1", len(runs))
	}
	if runs[0] != run {
		t.Fatalf("persisted run = %+v, want %+v", runs[0], run)
	}

	events, err := store.EventsByIssue(ctx, "exec-ci", "issue-1")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	if len(events) != 1 || events[0].Type != "ci.run" {
		t.Fatalf("events = %+v, want one ci.run event", events)
	}
}

func TestRecordConflictResolutionAttempt_ReloadsPublishedAttempt(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-conflict-attempt", "issue-1", domain.StateCIPending)

	attempt := storage.ConflictResolutionAttempt{
		ExecutionID:  "exec-conflict-attempt",
		IssueID:      "issue-1",
		PRNumber:     23,
		Branch:       "forge/exec-conflict-attempt/issue-1",
		OriginalSHA:  "abc123",
		CandidateSHA: "def456",
		Status:       storage.ConflictResolutionStatusPublished,
		Details:      "published automatic conflict replay",
		CreatedAt:    time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC),
	}
	if err := store.RecordConflictResolutionAttempt(ctx, attempt); err != nil {
		t.Fatalf("RecordConflictResolutionAttempt: %v", err)
	}

	got, err := store.ActiveConflictResolutionAttempt(ctx, "exec-conflict-attempt", "issue-1")
	if err != nil {
		t.Fatalf("ActiveConflictResolutionAttempt: %v", err)
	}
	if got != attempt {
		t.Fatalf("attempt = %+v, want %+v", got, attempt)
	}

	if err := store.UpdateConflictResolutionAttemptStatus(ctx, "exec-conflict-attempt", "issue-1", storage.ConflictResolutionStatusRestored, "restored after failed CI", time.Date(2026, 8, 31, 10, 5, 0, 0, time.UTC)); err != nil {
		t.Fatalf("UpdateConflictResolutionAttemptStatus: %v", err)
	}
	if _, err := store.ActiveConflictResolutionAttempt(ctx, "exec-conflict-attempt", "issue-1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("ActiveConflictResolutionAttempt after restore err = %v, want ErrNotFound", err)
	}
}

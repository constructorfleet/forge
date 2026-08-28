package storage_test

import (
	"context"
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

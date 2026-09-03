package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/storage"
)

// TestAgentRunExposesID verifies an AgentRun is addressable by the
// storage-assigned id it was created with (ADR 0030): AgentRunsByIssue and
// AgentRunsByExecution populate the run's ID field so a live reader can take
// that id straight into TranscriptEventsAfter without a separate lookup.
func TestAgentRunExposesID(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForAgentRun(t, store, "exec-id", "issue-id")

	runID, err := store.RecordAgentRun(ctx, storage.AgentRun{
		ExecutionID: "exec-id", IssueID: "issue-id",
		Backend: "claude-code", StartedAt: time.Now(), FinishedAt: time.Now(), Result: "IMPLEMENTED",
	})
	if err != nil {
		t.Fatalf("RecordAgentRun: %v", err)
	}

	byIssue, err := store.AgentRunsByIssue(ctx, "exec-id", "issue-id")
	if err != nil {
		t.Fatalf("AgentRunsByIssue: %v", err)
	}
	if len(byIssue) != 1 {
		t.Fatalf("got %d runs, want 1", len(byIssue))
	}
	if byIssue[0].ID != runID {
		t.Fatalf("AgentRunsByIssue run.ID = %d, want %d", byIssue[0].ID, runID)
	}

	byExec, err := store.AgentRunsByExecution(ctx, "exec-id")
	if err != nil {
		t.Fatalf("AgentRunsByExecution: %v", err)
	}
	if len(byExec) != 1 {
		t.Fatalf("got %d runs, want 1", len(byExec))
	}
	if byExec[0].ID != runID {
		t.Fatalf("AgentRunsByExecution run.ID = %d, want %d", byExec[0].ID, runID)
	}
}

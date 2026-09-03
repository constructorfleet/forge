package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/storage"
)

func TestRecordTranscriptEvents_PersistsAndRoundTripsInSeqOrder(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForAgentRun(t, store, "exec-transcript", "issue-transcript")

	runID, err := store.RecordAgentRun(ctx, storage.AgentRun{
		ExecutionID:  "exec-transcript",
		IssueID:      "issue-transcript",
		Backend:      "claude-code",
		StartedAt:    time.Now(),
		FinishedAt:   time.Now(),
		Result:       "IMPLEMENTED",
		ContextBytes: 10,
	})
	if err != nil {
		t.Fatalf("RecordAgentRun: %v", err)
	}

	events := []storage.TranscriptEvent{
		{Seq: 0, Type: "MESSAGE", Role: "assistant", Text: "Looking at the issue.", OccurredAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)},
		{Seq: 1, Type: "TOOL_CALL", Role: "assistant", ToolName: "Bash", ToolInput: `{"command":"go build ./..."}`, OccurredAt: time.Date(2026, 8, 28, 12, 0, 1, 0, time.UTC)},
		{Seq: 2, Type: "TOOL_RESULT", Role: "user", ToolName: "tool-1", ToolOutput: "build ok", OccurredAt: time.Date(2026, 8, 28, 12, 0, 2, 0, time.UTC)},
	}
	if err := store.RecordTranscriptEvents(ctx, "exec-transcript", "issue-transcript", runID, events); err != nil {
		t.Fatalf("RecordTranscriptEvents: %v", err)
	}

	got, err := store.TranscriptEventsByAgentRun(ctx, "exec-transcript", "issue-transcript", runID)
	if err != nil {
		t.Fatalf("TranscriptEventsByAgentRun: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	for i, want := range events {
		if got[i].Seq != want.Seq || got[i].Type != want.Type || got[i].Role != want.Role {
			t.Fatalf("event[%d] = %+v, want seq/type/role matching %+v", i, got[i], want)
		}
		if got[i].AgentRunID != runID {
			t.Fatalf("event[%d].AgentRunID = %d, want %d", i, got[i].AgentRunID, runID)
		}
		if !got[i].OccurredAt.Equal(want.OccurredAt) {
			t.Fatalf("event[%d].OccurredAt = %v, want %v", i, got[i].OccurredAt, want.OccurredAt)
		}
	}
	if got[0].Text != "Looking at the issue." {
		t.Fatalf("event[0].Text = %q", got[0].Text)
	}
	if got[1].ToolName != "Bash" || got[1].ToolInput != `{"command":"go build ./..."}` {
		t.Fatalf("event[1] = %+v", got[1])
	}
	if got[2].ToolOutput != "build ok" {
		t.Fatalf("event[2].ToolOutput = %q", got[2].ToolOutput)
	}
}

func TestRecordTranscriptEvents_EmptySliceIsNoop(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForAgentRun(t, store, "exec-transcript-empty", "issue-transcript-empty")

	runID, err := store.RecordAgentRun(ctx, storage.AgentRun{
		ExecutionID:  "exec-transcript-empty",
		IssueID:      "issue-transcript-empty",
		Backend:      "claude-code",
		StartedAt:    time.Now(),
		FinishedAt:   time.Now(),
		Result:       "IMPLEMENTED",
		ContextBytes: 10,
	})
	if err != nil {
		t.Fatalf("RecordAgentRun: %v", err)
	}

	if err := store.RecordTranscriptEvents(ctx, "exec-transcript-empty", "issue-transcript-empty", runID, nil); err != nil {
		t.Fatalf("RecordTranscriptEvents(nil): %v", err)
	}

	got, err := store.TranscriptEventsByAgentRun(ctx, "exec-transcript-empty", "issue-transcript-empty", runID)
	if err != nil {
		t.Fatalf("TranscriptEventsByAgentRun: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d events, want 0", len(got))
	}
}

func TestTranscriptEventsByIssue_OrdersAcrossMultipleAttempts(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForAgentRun(t, store, "exec-transcript-attempts", "issue-transcript-attempts")

	firstRunID, err := store.RecordAgentRun(ctx, storage.AgentRun{
		ExecutionID: "exec-transcript-attempts", IssueID: "issue-transcript-attempts",
		Backend: "claude-code", StartedAt: time.Now(), FinishedAt: time.Now(), Result: "FAILED",
	})
	if err != nil {
		t.Fatalf("RecordAgentRun (first attempt): %v", err)
	}
	if err := store.RecordTranscriptEvents(ctx, "exec-transcript-attempts", "issue-transcript-attempts", firstRunID, []storage.TranscriptEvent{
		{Seq: 0, Type: "MESSAGE", Text: "attempt one", OccurredAt: time.Now()},
	}); err != nil {
		t.Fatalf("RecordTranscriptEvents (first attempt): %v", err)
	}

	secondRunID, err := store.RecordAgentRun(ctx, storage.AgentRun{
		ExecutionID: "exec-transcript-attempts", IssueID: "issue-transcript-attempts",
		Backend: "claude-code", StartedAt: time.Now(), FinishedAt: time.Now(), Result: "IMPLEMENTED",
	})
	if err != nil {
		t.Fatalf("RecordAgentRun (second attempt): %v", err)
	}
	if err := store.RecordTranscriptEvents(ctx, "exec-transcript-attempts", "issue-transcript-attempts", secondRunID, []storage.TranscriptEvent{
		{Seq: 0, Type: "MESSAGE", Text: "attempt two", OccurredAt: time.Now()},
	}); err != nil {
		t.Fatalf("RecordTranscriptEvents (second attempt): %v", err)
	}

	got, err := store.TranscriptEventsByIssue(ctx, "exec-transcript-attempts", "issue-transcript-attempts")
	if err != nil {
		t.Fatalf("TranscriptEventsByIssue: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].AgentRunID != firstRunID || got[0].Text != "attempt one" {
		t.Fatalf("got[0] = %+v, want first attempt's event", got[0])
	}
	if got[1].AgentRunID != secondRunID || got[1].Text != "attempt two" {
		t.Fatalf("got[1] = %+v, want second attempt's event", got[1])
	}
}

func TestRecordTranscriptEvents_PersistsPhaseAndSubagent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForAgentRun(t, store, "exec-transcript-phase", "issue-transcript-phase")

	runID, err := store.RecordAgentRun(ctx, storage.AgentRun{
		ExecutionID:  "exec-transcript-phase",
		IssueID:      "issue-transcript-phase",
		Backend:      "claude-code",
		StartedAt:    time.Now(),
		FinishedAt:   time.Now(),
		Result:       "APPROVED",
		ContextBytes: 10,
	})
	if err != nil {
		t.Fatalf("RecordAgentRun: %v", err)
	}

	events := []storage.TranscriptEvent{
		{Seq: 0, Type: "MESSAGE", Role: "assistant", Text: "checking docs", Phase: "REVIEWING", Subagent: "docs", OccurredAt: time.Now()},
	}
	if err := store.RecordTranscriptEvents(ctx, "exec-transcript-phase", "issue-transcript-phase", runID, events); err != nil {
		t.Fatalf("RecordTranscriptEvents: %v", err)
	}

	got, err := store.TranscriptEventsByAgentRun(ctx, "exec-transcript-phase", "issue-transcript-phase", runID)
	if err != nil {
		t.Fatalf("TranscriptEventsByAgentRun: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].Phase != "REVIEWING" || got[0].Subagent != "docs" {
		t.Fatalf("event = %+v, want Phase=REVIEWING Subagent=docs", got[0])
	}
}

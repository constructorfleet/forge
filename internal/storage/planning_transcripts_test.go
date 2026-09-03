package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/storage"
)

// Planning-phase transcript capture (issue #248) reuses agent_runs,
// transcript_events, and events for Feature-scoped planning invocations,
// which have no execution_issues row to reference: `forge plan` runs against
// planning_executions, a deliberately separate table (0012). Migration 0020
// drops these three tables' foreign keys to executions/execution_issues so
// planning-scoped rows are writable; these tests pin that behaviour.

func TestStartAgentRun_AcceptsPlanningScopedIdentifiers(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	started := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	runID, err := store.StartAgentRun(ctx, storage.AgentRun{
		ExecutionID:  "feature-planning",
		IssueID:      "feature-planning",
		Backend:      "planning",
		StartedAt:    started,
		ContextBytes: 128,
	})
	if err != nil {
		t.Fatalf("StartAgentRun with planning identifiers: %v", err)
	}
	if runID <= 0 {
		t.Fatalf("StartAgentRun returned id = %d, want positive", runID)
	}

	if err := store.FinalizeAgentRun(ctx, runID, storage.AgentRun{
		ExecutionID: "feature-planning",
		IssueID:     "feature-planning",
		Backend:     "planning",
		StartedAt:   started,
		FinishedAt:  started.Add(time.Second),
		Result:      "OK",
	}); err != nil {
		t.Fatalf("FinalizeAgentRun with planning identifiers: %v", err)
	}

	// FinalizeAgentRun appends an "agent.run" Event, which exercises the
	// events table's dropped FK to executions in the same breath.
	events, err := store.EventsByIssue(ctx, "feature-planning", "feature-planning")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	if len(events) != 1 || events[0].Type != "agent.run" {
		t.Fatalf("events = %+v, want one agent.run event", events)
	}
}

func TestRecordTranscriptEvents_AcceptsPlanningScopedIdentifiers(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	started := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	runID, err := store.StartAgentRun(ctx, storage.AgentRun{
		ExecutionID: "feature-planning",
		IssueID:     "feature-planning",
		Backend:     "planning",
		StartedAt:   started,
	})
	if err != nil {
		t.Fatalf("StartAgentRun: %v", err)
	}

	want := []storage.TranscriptEvent{{
		Seq:        1,
		Type:       "MESSAGE",
		Role:       "assistant",
		Text:       "reviewing the spec",
		OccurredAt: started,
		Phase:      "planning",
		Subagent:   "specification-review",
	}}
	if err := store.RecordTranscriptEvents(ctx, "feature-planning", "feature-planning", runID, want); err != nil {
		t.Fatalf("RecordTranscriptEvents with planning identifiers: %v", err)
	}

	got, err := store.TranscriptEventsByIssue(ctx, "feature-planning", "feature-planning")
	if err != nil {
		t.Fatalf("TranscriptEventsByIssue: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d transcript events, want 1", len(got))
	}
	if got[0].Phase != "planning" || got[0].Subagent != "specification-review" || got[0].Text != "reviewing the spec" {
		t.Fatalf("persisted event = %+v, want the planning-tagged event back", got[0])
	}
	if got[0].AgentRunID != runID {
		t.Fatalf("AgentRunID = %d, want %d", got[0].AgentRunID, runID)
	}
}

func TestTranscriptEvents_StillRequireAnAgentRun(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// transcript_events.agent_run_id -> agent_runs(id) survives migration
	// 0020: planning agent_runs rows live in agent_runs too, so that FK
	// stays meaningful and must still reject an orphan transcript.
	err := store.RecordTranscriptEvents(ctx, "feature-planning", "feature-planning", 4242, []storage.TranscriptEvent{{
		Seq:        1,
		Type:       "MESSAGE",
		OccurredAt: time.Now(),
	}})
	if err == nil {
		t.Fatal("RecordTranscriptEvents against an unknown agent_run_id: want error, got nil")
	}
}

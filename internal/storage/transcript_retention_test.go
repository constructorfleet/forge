package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/storage"
)

// TestRecordTranscriptEvents_EnforcesSQLRetentionCap verifies the 5000-event
// per-run cap is enforced in SQL on the append path (ADR 0030): once a run
// exceeds it, the oldest seqs are deleted in the same transaction as the
// append, leaving the most recent 5000 events and seq gaps where eviction
// occurred.
func TestRecordTranscriptEvents_EnforcesSQLRetentionCap(t *testing.T) {
	const cap = 5000
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForAgentRun(t, store, "exec-cap", "issue-cap")

	runID, err := store.RecordAgentRun(ctx, storage.AgentRun{
		ExecutionID: "exec-cap", IssueID: "issue-cap",
		Backend: "claude-code", StartedAt: time.Now(), FinishedAt: time.Now(), Result: "IMPLEMENTED",
	})
	if err != nil {
		t.Fatalf("RecordAgentRun: %v", err)
	}

	// Insert cap+50 events with dense ascending seqs.
	events := make([]storage.TranscriptEvent, 0, cap+50)
	for i := 0; i < cap+50; i++ {
		events = append(events, storage.TranscriptEvent{
			Seq: i, Type: "MESSAGE", Text: "event",
			OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second),
		})
	}
	if err := store.RecordTranscriptEvents(ctx, "exec-cap", "issue-cap", runID, events); err != nil {
		t.Fatalf("RecordTranscriptEvents: %v", err)
	}

	got, err := store.TranscriptEventsByAgentRun(ctx, "exec-cap", "issue-cap", runID)
	if err != nil {
		t.Fatalf("TranscriptEventsByAgentRun: %v", err)
	}
	if len(got) != cap {
		t.Fatalf("got %d events after exceeding cap, want %d", len(got), cap)
	}
	// The retained events are the most recent: first seq 50, last seq cap+49.
	if got[0].Seq != 50 {
		t.Fatalf("got[0].Seq = %d, want 50 (oldest evicted, seq gap left behind)", got[0].Seq)
	}
	if got[len(got)-1].Seq != cap+49 {
		t.Fatalf("last seq = %d, want %d", got[len(got)-1].Seq, cap+49)
	}
	// Eviction must leave gaps: seq diverges from index.
	if got[0].Seq == 0 {
		t.Fatalf("eviction produced no seq gap; dense 0-based seq preserved")
	}
}

// TestRecordTranscriptEvents_DoesNotPersistTruncation verifies storage no
// longer persists TRUNCATION-marker events (ADR 0030): the marker is an
// in-memory recorder concept and must not land as a real row.
func TestRecordTranscriptEvents_DoesNotPersistTruncation(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForAgentRun(t, store, "exec-trunc", "issue-trunc")

	runID, err := store.RecordAgentRun(ctx, storage.AgentRun{
		ExecutionID: "exec-trunc", IssueID: "issue-trunc",
		Backend: "claude-code", StartedAt: time.Now(), FinishedAt: time.Now(), Result: "IMPLEMENTED",
	})
	if err != nil {
		t.Fatalf("RecordAgentRun: %v", err)
	}

	events := []storage.TranscriptEvent{
		{Seq: 0, Type: "TRUNCATION", Text: "5 dropped", OccurredAt: time.Now()},
		{Seq: 1, Type: "MESSAGE", Text: "real", OccurredAt: time.Now()},
	}
	if err := store.RecordTranscriptEvents(ctx, "exec-trunc", "issue-trunc", runID, events); err != nil {
		t.Fatalf("RecordTranscriptEvents: %v", err)
	}

	got, err := store.TranscriptEventsByAgentRun(ctx, "exec-trunc", "issue-trunc", runID)
	if err != nil {
		t.Fatalf("TranscriptEventsByAgentRun: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1 (TRUNCATION marker filtered)", len(got))
	}
	if got[0].Type != "MESSAGE" || got[0].Text != "real" {
		t.Fatalf("got[0] = %+v, want the real MESSAGE event only", got[0])
	}
}

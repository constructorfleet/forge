package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/storage"
)

// TestTranscriptEventsAfter_ReturnsEventsAfterSeqAndRespectsLimit covers the
// bounded tail API a live reader needs (ADR 0030): events strictly after
// afterSeq, in seq order, capped at limit.
func TestTranscriptEventsAfter_ReturnsEventsAfterSeqAndRespectsLimit(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForAgentRun(t, store, "exec-after", "issue-after")

	runID, err := store.RecordAgentRun(ctx, storage.AgentRun{
		ExecutionID: "exec-after", IssueID: "issue-after",
		Backend: "claude-code", StartedAt: time.Now(), FinishedAt: time.Now(), Result: "IMPLEMENTED",
	})
	if err != nil {
		t.Fatalf("RecordAgentRun: %v", err)
	}

	events := []storage.TranscriptEvent{
		{Seq: 0, Type: "MESSAGE", Text: "a", OccurredAt: time.Now()},
		{Seq: 1, Type: "MESSAGE", Text: "b", OccurredAt: time.Now()},
		{Seq: 2, Type: "MESSAGE", Text: "c", OccurredAt: time.Now()},
		{Seq: 3, Type: "MESSAGE", Text: "d", OccurredAt: time.Now()},
		{Seq: 4, Type: "MESSAGE", Text: "e", OccurredAt: time.Now()},
	}
	if err := store.RecordTranscriptEvents(ctx, "exec-after", "issue-after", runID, events); err != nil {
		t.Fatalf("RecordTranscriptEvents: %v", err)
	}

	// afterSeq=1, limit=2 => seq 2,3.
	got, err := store.TranscriptEventsAfter(ctx, runID, 1, 2)
	if err != nil {
		t.Fatalf("TranscriptEventsAfter: %v", err)
	}
	if len(got) != 2 || got[0].Seq != 2 || got[1].Seq != 3 {
		t.Fatalf("got %+v, want seqs [2 3]", got)
	}

	// afterSeq=3 => only seq 4.
	got, err = store.TranscriptEventsAfter(ctx, runID, 3, 10)
	if err != nil {
		t.Fatalf("TranscriptEventsAfter(afterSeq=3): %v", err)
	}
	if len(got) != 1 || got[0].Seq != 4 {
		t.Fatalf("got %+v, want [seq 4]", got)
	}

	// afterSeq=4 => nothing yet.
	got, err = store.TranscriptEventsAfter(ctx, runID, 4, 10)
	if err != nil {
		t.Fatalf("TranscriptEventsAfter(afterSeq=4): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d events after last seq, want 0", len(got))
	}
}

// TestTranscriptEventsAfter_CursorSurvivesEviction verifies a reader holding
// an afterSeq cursor is not invalidated when the SQL retention cap evicts
// older events (ADR 0030): stepping the cursor still returns the events that
// follow, even across a gap left by eviction.
func TestTranscriptEventsAfter_CursorSurvivesEviction(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForAgentRun(t, store, "exec-evict", "issue-evict")

	runID, err := store.RecordAgentRun(ctx, storage.AgentRun{
		ExecutionID: "exec-evict", IssueID: "issue-evict",
		Backend: "claude-code", StartedAt: time.Now(), FinishedAt: time.Now(), Result: "IMPLEMENTED",
	})
	if err != nil {
		t.Fatalf("RecordAgentRun: %v", err)
	}

	// Fill the run past the SQL cap so the oldest are evicted.
	over := storage.MaxTranscriptEventsPerRun + 25
	big := make([]storage.TranscriptEvent, 0, over)
	for i := 0; i < over; i++ {
		big = append(big, storage.TranscriptEvent{
			Seq: i, Type: "MESSAGE", Text: "x",
			OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second),
		})
	}
	if err := store.RecordTranscriptEvents(ctx, "exec-evict", "issue-evict", runID, big); err != nil {
		t.Fatalf("RecordTranscriptEvents: %v", err)
	}

	// First seq in storage is now 25 (seqs 0..24 evicted), so a cursor at
	// 20 landed in the eviction gap yet must still read onward from 25.
	got, err := store.TranscriptEventsAfter(ctx, runID, 20, 5)
	if err != nil {
		t.Fatalf("TranscriptEventsAfter: %v", err)
	}
	if len(got) != 5 || got[0].Seq != 25 || got[4].Seq != 29 {
		t.Fatalf("got %+v, want seqs [25 26 27 28 29] (cursor survives eviction gap)", got)
	}
}

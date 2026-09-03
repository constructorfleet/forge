package engine

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/storage"
)

// recordingTranscriptStore is a transcriptStore double that records every
// RecordTranscriptEvents append, optionally failing to prove best-effort
// capture drops events instead of advancing the flush watermark.
type recordingTranscriptStore struct {
	mu    sync.Mutex
	calls [][]storage.TranscriptEvent
	fail  bool
}

func (s *recordingTranscriptStore) RecordTranscriptEvents(_ context.Context, _, _ string, _ int64, events []storage.TranscriptEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("store down")
	}
	s.calls = append(s.calls, events)
	return nil
}

// TestSinkFlush_AppendsOnlyUnflushed asserts the sink writes only events
// with Seq above its high-water mark: re-flushing must never re-append an
// already-persisted Seq (the UNIQUE(agent_run_id, seq) constraint).
func TestSinkFlush_AppendsOnlyUnflushed(t *testing.T) {
	store := &recordingTranscriptStore{}
	sink := newPersistingTranscriptSink(context.Background(), store, "exec", "issue", 7, "phase", "sub", nil)

	emit := func(text string) {
		sink.recorder.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: text})
	}
	emit("a")
	emit("b")
	sink.flush()

	if len(store.calls) != 1 {
		t.Fatalf("got %d flush calls, want 1", len(store.calls))
	}
	if got := store.calls[0]; len(got) != 2 || got[0].Seq != 0 || got[1].Seq != 1 {
		t.Fatalf("first flush = %+v, want seqs 0,1", got)
	}

	// A second flush with no new events must append nothing.
	sink.flush()
	if len(store.calls) != 1 {
		t.Fatalf("got %d flush calls after no-op reflush, want 1", len(store.calls))
	}

	// New events flush only their own, higher seqs.
	emit("c")
	sink.flush()
	if len(store.calls) != 2 {
		t.Fatalf("got %d flush calls, want 2", len(store.calls))
	}
	if got := store.calls[1]; len(got) != 1 || got[0].Seq != 2 {
		t.Fatalf("second flush = %+v, want only seq 2", got)
	}
}

// TestSinkFlush_BestEffortDropsOnError asserts a storage failure does not
// advance the watermark, so the next successful flush still appends the
// dropped events.
func TestSinkFlush_BestEffortDropsOnError(t *testing.T) {
	store := &recordingTranscriptStore{}
	sink := newPersistingTranscriptSink(context.Background(), store, "exec", "issue", 7, "phase", "sub", nil)

	sink.recorder.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "a"})
	store.fail = true
	sink.flush()
	if len(store.calls) != 0 {
		t.Fatalf("got %d flush calls, want 0 under failing store", len(store.calls))
	}

	store.fail = false
	sink.flush()
	if len(store.calls) != 1 || len(store.calls[0]) != 1 || store.calls[0][0].Seq != 0 {
		t.Fatalf("post-recovery flush = %+v, want the dropped seq 0 appended once", store.calls)
	}
}

// TestSinkClose_FlushesOnCancelImmuneContext asserts Close persists the whole
// tail even when the sink's context is already cancelled, proving the final
// flush runs on the cancel-immune context so a cancelled run keeps its
// transcript up to the kill.
func TestSinkClose_FlushesOnCancelImmuneContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &recordingTranscriptStore{}
	sink := newPersistingTranscriptSink(ctx, store, "exec", "issue", 7, "phase", "sub", nil)

	sink.recorder.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "a"})
	cancel()
	sink.Close()

	if len(store.calls) != 1 || len(store.calls[0]) != 1 || store.calls[0][0].Text != "a" {
		t.Fatalf("Close flush = %+v, want the event appended despite cancelled context", store.calls)
	}
}

// TestSinkClose_DoesNotDoubleAppend asserts a close after an in-flight flush
// does not re-append already-flushed seqs, and that emitting into a closed
// sink is a no-op.
func TestSinkClose_DoesNotDoubleAppend(t *testing.T) {
	store := &recordingTranscriptStore{}
	sink := newPersistingTranscriptSink(context.Background(), store, "exec", "issue", 7, "phase", "sub", nil)

	sink.recorder.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "a"})
	sink.flush()
	sink.Close()

	if len(store.calls) != 1 {
		t.Fatalf("got %d flush calls, want exactly 1 (no double-append on Close)", len(store.calls))
	}

	sink.recorder.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "b"})
	sink.Close()
	if len(store.calls) != 1 {
		t.Fatalf("got %d flush calls after emitting into closed sink, want 1", len(store.calls))
	}
}

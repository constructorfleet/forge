package planningagent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/storage"
)

// recordingTranscriptStore is a TranscriptStore double that records every
// RecordTranscriptEvents append, optionally failing to prove best-effort
// capture drops events instead of advancing the flush watermark.
type recordingTranscriptStore struct {
	mu          sync.Mutex
	calls       [][]storage.TranscriptEvent
	fail        bool
	deadlineSet bool
	deadline    time.Time
}

// RecordTranscriptEvents rejects a cancelled context first, the way
// database/sql does: BeginTx checks ctx.Done() before it takes a connection.
func (s *recordingTranscriptStore) RecordTranscriptEvents(ctx context.Context, _, _ string, _ int64, events []storage.TranscriptEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		s.deadlineSet = true
		s.deadline = deadline
	}
	if s.fail {
		return errors.New("store down")
	}
	s.calls = append(s.calls, events)
	return nil
}

func (s *recordingTranscriptStore) StartAgentRun(context.Context, storage.AgentRun) (int64, error) {
	return 0, nil
}

func (s *recordingTranscriptStore) FinalizeAgentRun(context.Context, int64, storage.AgentRun) error {
	return nil
}

// TestPlanningSinkFlush_AppendsOnlyUnflushed mirrors the engine sink test:
// the sink appends only events above its high-water mark, never re-appending
// an already-persisted Seq.
func TestPlanningSinkFlush_AppendsOnlyUnflushed(t *testing.T) {
	store := &recordingTranscriptStore{}
	sink := newPersistingTranscriptSink(context.Background(), store, "exec", "issue", 7, "k", nil)

	sink.recorder.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "a"})
	sink.recorder.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "b"})
	sink.flush()

	if len(store.calls) != 1 {
		t.Fatalf("got %d flush calls, want 1", len(store.calls))
	}
	if got := store.calls[0]; len(got) != 2 || got[0].Seq != 0 || got[1].Seq != 1 || got[0].Phase != "planning" {
		t.Fatalf("first flush = %+v, want seqs 0,1 tagged planning", got)
	}

	sink.flush()
	if len(store.calls) != 1 {
		t.Fatalf("got %d flush calls after no-op reflush, want 1", len(store.calls))
	}

	sink.recorder.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "c"})
	sink.flush()
	if len(store.calls) != 2 {
		t.Fatalf("got %d flush calls, want 2", len(store.calls))
	}
	if got := store.calls[1]; len(got) != 1 || got[0].Seq != 2 {
		t.Fatalf("second flush = %+v, want only seq 2", got)
	}
}

// TestPlanningSinkFlush_PersistsAfterContextCancellation is issue 454, the
// planning counterpart: a debounced flush of a post-cancellation event lands in
// storage without waiting for Close, so a cancelled planning invocation keeps
// its diagnostic fallback instead of reading as blank.
func TestPlanningSinkFlush_PersistsAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &recordingTranscriptStore{}
	sink := newPersistingTranscriptSink(ctx, store, "exec", "issue", 7, "k", nil)

	cancel()
	sink.recorder.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "cancelled before output"})
	sink.flush()

	if len(store.calls) != 1 || len(store.calls[0]) != 1 || store.calls[0][0].Text != "cancelled before output" {
		t.Fatalf("flush after cancellation = %+v, want the fallback event appended", store.calls)
	}
}

// TestPlanningSinkClose_FlushesOnCancelImmuneContext asserts Close persists
// the tail on a cancel-immune context, so a cancelled planning invocation
// keeps its transcript.
func TestPlanningSinkClose_FlushesOnCancelImmuneContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &recordingTranscriptStore{}
	sink := newPersistingTranscriptSink(ctx, store, "exec", "issue", 7, "k", nil)

	sink.recorder.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "a"})
	cancel()
	sink.Close()

	if len(store.calls) != 1 || len(store.calls[0]) != 1 || store.calls[0][0].Text != "a" {
		t.Fatalf("Close flush = %+v, want the event appended despite cancelled context", store.calls)
	}
}

// TestPlanningSinkFlush_UsesBoundedCancelImmuneContext asserts a stuck store
// gets a flush-specific deadline. The context must outlive caller
// cancellation, but it must not live forever.
func TestPlanningSinkFlush_UsesBoundedCancelImmuneContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &recordingTranscriptStore{}
	sink := newPersistingTranscriptSink(ctx, store, "exec", "issue", 7, "k", nil)

	cancel()
	before := time.Now()
	sink.recorder.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "a"})
	sink.flush()

	if !store.deadlineSet {
		t.Fatalf("flush context has no deadline")
	}
	if until := store.deadline.Sub(before); until <= 0 || until > 5*time.Second+100*time.Millisecond {
		t.Fatalf("flush context deadline is %s from start, want about 5s", until)
	}
}

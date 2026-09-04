package tui_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
)

// The real Store must satisfy the tailer's read seam directly.
var _ tui.TranscriptStore = (storage.Store)(nil)

// fakeTranscriptStore is a TranscriptStore double over a fixed event log: it
// answers each tail read from the log, so the cursor semantics under test are
// the store's own (seq strictly greater than afterSeq).
type fakeTranscriptStore struct {
	events []storage.TranscriptEvent
	err    error
	// calls records every afterSeq the tailer read with.
	calls []int64
}

func (f *fakeTranscriptStore) TranscriptEventsAfter(_ context.Context, _, afterSeq, limit int64) ([]storage.TranscriptEvent, error) {
	f.calls = append(f.calls, afterSeq)
	if f.err != nil {
		return nil, f.err
	}
	var out []storage.TranscriptEvent
	for _, e := range f.events {
		if int64(e.Seq) > afterSeq {
			out = append(out, e)
		}
		if int64(len(out)) == limit {
			break
		}
	}
	return out, nil
}

// msg builds a MESSAGE event at seq.
func msg(seq int, text string) storage.TranscriptEvent {
	return storage.TranscriptEvent{Seq: seq, Type: "MESSAGE", Role: "assistant", Text: text}
}

// TestTailerPollReadsFromFirstSeq proves the first Poll reads the whole
// retained history, including the seq-0 event: an unattached cursor must not
// be 0, or the tail API's strict "greater than" would skip it.
func TestTailerPollReadsFromFirstSeq(t *testing.T) {
	store := &fakeTranscriptStore{events: []storage.TranscriptEvent{
		msg(0, "hello"),
		{Seq: 1, Type: "TOOL_CALL", ToolName: "read", ToolInput: "f.go"},
	}}

	tailer := tui.NewTranscriptTailer(store, 7, 100)
	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(vm.Events) != 2 {
		t.Fatalf("Events len = %d, want 2", len(vm.Events))
	}
	if vm.Events[0].Seq != 0 || vm.Events[0].Text != "hello" {
		t.Fatalf("Events[0] = %+v, want seq 0/hello", vm.Events[0])
	}
	if vm.Events[1].ToolName != "read" || vm.Events[1].ToolInput != "f.go" {
		t.Fatalf("Events[1] = %+v, want tool read/f.go", vm.Events[1])
	}
	if vm.Evicted {
		t.Fatalf("Evicted = true, want false with history from seq 0")
	}
}

// TestTailerPollAdvancesCursor proves the second Poll reads only the events
// appended since the last pass.
func TestTailerPollAdvancesCursor(t *testing.T) {
	store := &fakeTranscriptStore{events: []storage.TranscriptEvent{msg(0, "first"), msg(1, "second")}}
	tailer := tui.NewTranscriptTailer(store, 7, 100)

	if _, err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll 1: %v", err)
	}
	if got := tailer.Cursor(); got != 1 {
		t.Fatalf("Cursor = %d, want 1", got)
	}

	store.events = append(store.events, msg(2, "third"))
	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll 2: %v", err)
	}
	if len(vm.Events) != 3 || vm.Retained != 3 {
		t.Fatalf("Events len = %d, Retained = %d, want 3/3", len(vm.Events), vm.Retained)
	}
	if store.calls[1] != 1 {
		t.Fatalf("second read afterSeq = %d, want 1", store.calls[1])
	}
}

// TestTailerBufferRespectsCap proves the ring bounds memory and reports the
// eviction, so the pane can mark the gap.
func TestTailerBufferRespectsCap(t *testing.T) {
	store := &fakeTranscriptStore{events: []storage.TranscriptEvent{
		msg(0, "a"), msg(1, "b"), msg(2, "c"), msg(3, "d"),
	}}

	tailer := tui.NewTranscriptTailer(store, 7, 3)
	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if vm.Retained != 3 {
		t.Fatalf("Retained = %d, want 3", vm.Retained)
	}
	if vm.Events[0].Seq != 1 || vm.Events[2].Seq != 3 {
		t.Fatalf("Events = %v, want seqs [1 2 3] (0 evicted)", vm.Events)
	}
	if vm.Dropped != 1 || !vm.Evicted {
		t.Fatalf("Dropped = %d, Evicted = %v, want 1/true", vm.Dropped, vm.Evicted)
	}
}

// TestTailerReattachBackfillsFromRetainedStart proves a reattach reads the
// store's retained history again and marks the eviction when that history no
// longer starts at seq 0.
func TestTailerReattachBackfillsFromRetainedStart(t *testing.T) {
	store := &fakeTranscriptStore{events: []storage.TranscriptEvent{msg(0, "a"), msg(1, "b")}}
	tailer := tui.NewTranscriptTailer(store, 7, 100)
	if _, err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll 1: %v", err)
	}

	// The store's retention window drops the early seqs while the reader is away.
	store.events = []storage.TranscriptEvent{msg(4, "e"), msg(5, "f")}
	tailer.Reattach(7)
	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll after reattach: %v", err)
	}
	if len(vm.Events) != 2 || vm.Events[0].Seq != 4 {
		t.Fatalf("Events = %v, want retained start at seq 4", vm.Events)
	}
	if !vm.Evicted {
		t.Fatalf("Evicted = false, want true: earlier events are not retained")
	}
	if vm.Dropped != 0 {
		t.Fatalf("Dropped = %d, want 0: the store evicted, not the ring", vm.Dropped)
	}
}

// TestTailerReadFailureKeepsWindow proves a wedged store degrades to a stale
// window: the retained events and the cursor survive, so the next pass
// resumes and no read failure can reach the capture path.
func TestTailerReadFailureKeepsWindow(t *testing.T) {
	store := &fakeTranscriptStore{events: []storage.TranscriptEvent{msg(0, "a")}}
	tailer := tui.NewTranscriptTailer(store, 7, 100)
	if _, err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll 1: %v", err)
	}

	store.err = errors.New("wedged")
	vm, err := tailer.Poll(context.Background())
	if err == nil {
		t.Fatalf("Poll 2 err = nil, want the read failure")
	}
	if len(vm.Events) != 1 {
		t.Fatalf("Events len = %d, want the last good window", len(vm.Events))
	}
	if got := tailer.Cursor(); got != 0 {
		t.Fatalf("Cursor = %d, want 0 unchanged", got)
	}

	store.err = nil
	store.events = append(store.events, msg(1, "b"))
	vm, err = tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll 3: %v", err)
	}
	if len(vm.Events) != 2 {
		t.Fatalf("Events len = %d, want 2 after recovery", len(vm.Events))
	}
}

// TestTailerScrollbackWithinBuffer proves the viewport scrolls back inside
// the retained window and returns to the tail.
func TestTailerScrollbackWithinBuffer(t *testing.T) {
	store := &fakeTranscriptStore{}
	for i := range 10 {
		store.events = append(store.events, msg(i, "e"))
	}
	tailer := tui.NewTranscriptTailer(store, 7, 100)
	tailer.SetHeight(4)

	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(vm.Events) != 4 || vm.Events[0].Seq != 6 || !vm.AtTail {
		t.Fatalf("tail window = %v (AtTail %v), want seqs [6..9]", vm.Events, vm.AtTail)
	}

	tailer.ScrollUp(3)
	vm, _ = tailer.Poll(context.Background())
	if vm.Events[0].Seq != 3 || vm.Events[3].Seq != 6 {
		t.Fatalf("scrolled window = %v, want seqs [3..6]", vm.Events)
	}
	if vm.AtTail {
		t.Fatalf("AtTail = true, want false while scrolled back")
	}

	// Scrollback clamps at the retained start.
	tailer.ScrollUp(100)
	vm, _ = tailer.Poll(context.Background())
	if vm.Events[0].Seq != 0 {
		t.Fatalf("clamped window = %v, want the retained start", vm.Events)
	}

	tailer.ScrollToTail()
	vm, _ = tailer.Poll(context.Background())
	if !vm.AtTail || vm.Events[3].Seq != 9 {
		t.Fatalf("window = %v (AtTail %v), want the tail", vm.Events, vm.AtTail)
	}
}

// TestTailerScrollAnchorHoldsAsTailAdvances proves new events do not drag a
// scrolled-back viewport forward.
func TestTailerScrollAnchorHoldsAsTailAdvances(t *testing.T) {
	store := &fakeTranscriptStore{}
	for i := range 8 {
		store.events = append(store.events, msg(i, "e"))
	}
	tailer := tui.NewTranscriptTailer(store, 7, 100)
	tailer.SetHeight(3)
	if _, err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll 1: %v", err)
	}
	tailer.ScrollUp(2)

	store.events = append(store.events, msg(8, "new"), msg(9, "newer"))
	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll 2: %v", err)
	}
	if vm.Events[0].Seq != 3 || vm.Events[2].Seq != 5 {
		t.Fatalf("window = %v, want the anchor held at seqs [3..5]", vm.Events)
	}
}

// TestTailerEmptyPoll proves a run with no events yields an empty window.
func TestTailerEmptyPoll(t *testing.T) {
	tailer := tui.NewTranscriptTailer(&fakeTranscriptStore{}, 7, 100)

	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(vm.Events) != 0 || vm.Evicted {
		t.Fatalf("vm = %+v, want an empty unmarked window", vm)
	}
}

// TestTailerRingCapDefaults proves a non-positive cap falls back to the
// default rather than leaving the buffer unbounded.
func TestTailerRingCapDefaults(t *testing.T) {
	store := &fakeTranscriptStore{}
	for i := range 3 {
		store.events = append(store.events, msg(i, "e"))
	}
	tailer := tui.NewTranscriptTailer(store, 7, 0)
	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if vm.Retained != 3 || vm.Dropped != 0 {
		t.Fatalf("vm = %+v, want 3 retained and no drops", vm)
	}
}

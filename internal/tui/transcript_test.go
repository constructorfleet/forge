package tui_test

import (
	"context"
	"errors"
	"testing"
	"time"

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
	if got := tailer.LatestRunCursor(); got != 1 {
		t.Fatalf("LatestRunCursor = %d, want 1", got)
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

// TestTailerPollCarriesOccurredAt proves Poll copies the stored event's finish
// time onto the TUI's own TranscriptEvent, so the pane can order a gate row
// into the timeline against it.
func TestTailerPollCarriesOccurredAt(t *testing.T) {
	at := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	store := &fakeTranscriptStore{events: []storage.TranscriptEvent{
		{Seq: 0, Type: "MESSAGE", OccurredAt: at},
	}}
	tailer := tui.NewTranscriptTailer(store, 7, 100)
	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if !vm.Events[0].OccurredAt.Equal(at) {
		t.Fatalf("OccurredAt = %v, want %v", vm.Events[0].OccurredAt, at)
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
	if got := tailer.LatestRunCursor(); got != 0 {
		t.Fatalf("LatestRunCursor = %d, want 0 unchanged", got)
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

// TestTailerScrollAnchorHoldsWhenAppendEvictsAtCap proves the scrollback stays
// anchored on the same event when a poll appends events while the ring sits at
// its cap: each append front-evicts one retained event, so the retained length
// does not grow. A naive offset += len(events) drifts the window towards the
// retained start instead of holding it, because it counts events read, not the
// ring's actual growth.
func TestTailerScrollAnchorHoldsWhenAppendEvictsAtCap(t *testing.T) {
	store := &fakeTranscriptStore{}
	for i := range 4 {
		store.events = append(store.events, msg(i, "e"))
	}
	tailer := tui.NewTranscriptTailer(store, 7, 4)
	tailer.SetHeight(2)
	if _, err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll 1: %v", err)
	}
	tailer.ScrollUp(1)

	// The ring is already at its cap of 4. Two more events each evict one
	// retained event, so the retained length stays at 4 across the poll.
	store.events = append(store.events, msg(4, "new"), msg(5, "newer"))
	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll 2: %v", err)
	}
	if vm.Retained != 4 {
		t.Fatalf("Retained = %d, want 4 (cap holds, no growth)", vm.Retained)
	}
	if len(vm.Events) != 2 || vm.Events[0].Seq != 2 || vm.Events[1].Seq != 3 {
		t.Fatalf("window = %v, want the anchor's successor held at seqs [2 3]", vm.Events)
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

// fakeMultiRunStore is a TranscriptStore double over several runs' logs, keyed
// by AgentRunID: it proves the tailer reads each attempt with its own cursor.
type fakeMultiRunStore struct {
	runs map[int64][]storage.TranscriptEvent
	// errFor fails the read for one run, so a pass can fail part way.
	errFor map[int64]error
	// calls records every (run, afterSeq) pair the tailer read with.
	calls [][2]int64
}

func (f *fakeMultiRunStore) TranscriptEventsAfter(_ context.Context, runID, afterSeq, limit int64) ([]storage.TranscriptEvent, error) {
	f.calls = append(f.calls, [2]int64{runID, afterSeq})
	if err := f.errFor[runID]; err != nil {
		return nil, err
	}
	var out []storage.TranscriptEvent
	for _, e := range f.runs[runID] {
		if int64(e.Seq) > afterSeq {
			out = append(out, e)
		}
		if int64(len(out)) == limit {
			break
		}
	}
	return out, nil
}

// runMsg builds a MESSAGE event at seq within agentRunID.
func runMsg(runID int64, seq int, text string) storage.TranscriptEvent {
	e := msg(seq, text)
	e.AgentRunID = runID
	return e
}

// TestTailerAddRunKeepsEarlierAttempts proves a retry extends the scrollback
// instead of replacing it: both attempts stay retained, in run insertion order.
func TestTailerAddRunKeepsEarlierAttempts(t *testing.T) {
	store := &fakeMultiRunStore{runs: map[int64][]storage.TranscriptEvent{
		4: {runMsg(4, 0, "first try"), runMsg(4, 1, "failed")},
		9: {runMsg(9, 0, "second try")},
	}}

	tailer := tui.NewTranscriptTailer(store, 4, 100)
	if _, err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	tailer.AddRun(9)
	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll after AddRun: %v", err)
	}

	want := []string{"first try", "failed", "second try"}
	if len(vm.Events) != len(want) {
		t.Fatalf("Events = %d, want %d: %+v", len(vm.Events), len(want), vm.Events)
	}
	for i, text := range want {
		if vm.Events[i].Text != text {
			t.Errorf("Events[%d].Text = %q, want %q", i, vm.Events[i].Text, text)
		}
	}
	if vm.Events[2].AgentRunID != 9 {
		t.Errorf("Events[2].AgentRunID = %d, want 9", vm.Events[2].AgentRunID)
	}
	if len(vm.RunOrder) != 2 || vm.RunOrder[0] != 4 || vm.RunOrder[1] != 9 {
		t.Errorf("RunOrder = %v, want [4 9]", vm.RunOrder)
	}
}

// TestTailerAddRunReadsNewRunFromFirstSeq proves each attempt carries its own
// cursor: the new run's seq-0 event is not skipped by the older run's cursor.
func TestTailerAddRunReadsNewRunFromFirstSeq(t *testing.T) {
	store := &fakeMultiRunStore{runs: map[int64][]storage.TranscriptEvent{
		4: {runMsg(4, 0, "a"), runMsg(4, 1, "b"), runMsg(4, 2, "c")},
		9: {runMsg(9, 0, "retry")},
	}}

	tailer := tui.NewTranscriptTailer(store, 4, 100)
	if _, err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	tailer.AddRun(9)
	if _, err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll after AddRun: %v", err)
	}

	if got := store.calls[len(store.calls)-1]; got != [2]int64{9, -1} {
		t.Errorf("last read = run %d after seq %d, want run 9 after seq -1", got[0], got[1])
	}
}

// TestTailerAddRunIgnoresAKnownRun proves AddRun is idempotent, so a repeated
// roster poll cannot duplicate an attempt or its divider.
func TestTailerAddRunIgnoresAKnownRun(t *testing.T) {
	store := &fakeMultiRunStore{runs: map[int64][]storage.TranscriptEvent{4: {runMsg(4, 0, "a")}}}

	tailer := tui.NewTranscriptTailer(store, 4, 100)
	tailer.AddRun(4)
	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(vm.RunOrder) != 1 || len(vm.Events) != 1 {
		t.Errorf("RunOrder = %v, Events = %+v, want one run and one event", vm.RunOrder, vm.Events)
	}
}

// TestTailerReattachDropsEarlierAttempts proves Reattach still starts a fresh
// history: it is the reader-gap recovery, not the retry path.
func TestTailerReattachDropsEarlierAttempts(t *testing.T) {
	store := &fakeMultiRunStore{runs: map[int64][]storage.TranscriptEvent{
		4: {runMsg(4, 0, "first try")},
		9: {runMsg(9, 0, "second try")},
	}}

	tailer := tui.NewTranscriptTailer(store, 4, 100)
	if _, err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	tailer.Reattach(9)
	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll after Reattach: %v", err)
	}

	if len(vm.Events) != 1 || vm.Events[0].Text != "second try" {
		t.Errorf("Events = %+v, want the second attempt alone", vm.Events)
	}
	if len(vm.RunOrder) != 1 || vm.RunOrder[0] != 9 {
		t.Errorf("RunOrder = %v, want [9]", vm.RunOrder)
	}
}

// TestTailerPartialPollHoldsScrollAnchor proves a read failure on a later
// attempt still commits the bookkeeping for the events it already appended: a
// scrolled-back window must not slide under the operator.
func TestTailerPartialPollHoldsScrollAnchor(t *testing.T) {
	store := &fakeMultiRunStore{runs: map[int64][]storage.TranscriptEvent{
		4: {runMsg(4, 0, "a"), runMsg(4, 1, "b"), runMsg(4, 2, "c")},
	}}
	store.errFor = map[int64]error{9: errors.New("boom")}

	tailer := tui.NewTranscriptTailer(store, 4, 100)
	tailer.SetHeight(2)
	if _, err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	tailer.ScrollUp(1)
	tailer.AddRun(9)
	store.runs[4] = append(store.runs[4], runMsg(4, 3, "d"))

	vm, err := tailer.Poll(context.Background())
	if err == nil {
		t.Fatal("Poll: want the failed attempt's error")
	}
	if len(vm.Events) != 2 || vm.Events[0].Text != "a" || vm.Events[1].Text != "b" {
		t.Errorf("Events = %+v, want the anchored window [a b]", vm.Events)
	}
}

// TestTailerScrollAnchorHoldsAcrossAnEarlierAttempt proves the scrollback
// anchors on an event, not on a distance from the tail: a late event that sorts
// in ahead of the visible window must not slide the window.
func TestTailerScrollAnchorHoldsAcrossAnEarlierAttempt(t *testing.T) {
	store := &fakeMultiRunStore{runs: map[int64][]storage.TranscriptEvent{
		4: {runMsg(4, 0, "a"), runMsg(4, 1, "b"), runMsg(4, 2, "c")},
		9: {runMsg(9, 0, "p"), runMsg(9, 1, "q"), runMsg(9, 2, "r"), runMsg(9, 3, "s")},
	}}

	tailer := tui.NewTranscriptTailer(store, 4, 100)
	tailer.AddRun(9)
	tailer.SetHeight(2)
	if _, err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll 1: %v", err)
	}
	tailer.ScrollUp(2)
	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll 2: %v", err)
	}
	if vm.Events[0].Text != "p" || vm.Events[1].Text != "q" {
		t.Fatalf("scrolled window = %+v, want [p q]", vm.Events)
	}

	// The earlier attempt reports one more event, which sorts before the window.
	store.runs[4] = append(store.runs[4], runMsg(4, 3, "late"))
	vm, err = tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll 3: %v", err)
	}
	if len(vm.Events) != 2 || vm.Events[0].Text != "p" || vm.Events[1].Text != "q" {
		t.Fatalf("window = %+v, want the anchor held at [p q]", vm.Events)
	}
}

// TestTailerEvictedAnchorUsesRetainedSuccessor proves that a lost
// scroll anchor recovers at the next retained event. A late earlier attempt can
// sort before the visible window while the ring drops by append order.
func TestTailerEvictedAnchorUsesRetainedSuccessor(t *testing.T) {
	store := &fakeMultiRunStore{runs: map[int64][]storage.TranscriptEvent{
		4: {runMsg(4, 0, "a"), runMsg(4, 1, "b")},
		9: {runMsg(9, 0, "p"), runMsg(9, 1, "q"), runMsg(9, 2, "r"), runMsg(9, 3, "s")},
	}}

	tailer := tui.NewTranscriptTailer(store, 4, 6)
	tailer.AddRun(9)
	tailer.SetHeight(2)
	if _, err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll 1: %v", err)
	}
	tailer.ScrollUp(2)
	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll 2: %v", err)
	}
	if len(vm.Events) != 2 || vm.Events[0].Text != "p" || vm.Events[1].Text != "q" {
		t.Fatalf("scrolled window = %+v, want [p q]", vm.Events)
	}

	store.runs[4] = append(store.runs[4],
		runMsg(4, 2, "c"),
		runMsg(4, 3, "d"),
		runMsg(4, 4, "e"),
	)
	vm, err = tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll 3: %v", err)
	}
	if len(vm.Events) != 2 || vm.Events[0].Text != "q" || vm.Events[1].Text != "r" {
		t.Fatalf("window = %+v, want the retained successor window [q r]", vm.Events)
	}
}

// TestTailerOrdersLateEventByRunInsertion proves the retained window reads in
// run insertion order: a late event from an earlier attempt sorts back into its
// own attempt instead of adding a second, out-of-order divider.
func TestTailerOrdersLateEventByRunInsertion(t *testing.T) {
	store := &fakeMultiRunStore{runs: map[int64][]storage.TranscriptEvent{
		4: {runMsg(4, 0, "first try")},
		9: {runMsg(9, 0, "second try")},
	}}

	tailer := tui.NewTranscriptTailer(store, 4, 100)
	tailer.AddRun(9)
	if _, err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	store.runs[4] = append(store.runs[4], runMsg(4, 1, "late failure"))

	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}

	want := []string{"first try", "late failure", "second try"}
	if len(vm.Events) != len(want) {
		t.Fatalf("Events = %+v, want %v", vm.Events, want)
	}
	for i, text := range want {
		if vm.Events[i].Text != text {
			t.Errorf("Events[%d].Text = %q, want %q", i, vm.Events[i].Text, text)
		}
	}
}

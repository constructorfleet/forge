package tui

// Package tui/transcript.go implements the live transcript tailing pipeline:
// a poller over the Store's bounded tail API that feeds a capped ring buffer
// and a scrollback window. The read path is wholly separate from the capture
// path, so a wedged or slow reader can never stall an Execution (ADR 0030):
// the worst it produces is a gap the view marks.

import (
	"context"
	"fmt"
	"sort"

	"github.com/Teagan42/forge/internal/storage"
)

// TranscriptStore is the read-only slice of storage the transcript tailer
// needs: the bounded tail API that returns events after a cursor.
type TranscriptStore interface {
	TranscriptEventsAfter(ctx context.Context, agentRunID, afterSeq, limit int64) ([]storage.TranscriptEvent, error)
}

// defaultTranscriptRing caps the retained events per tailer. Smaller than
// the store's MaxTranscriptEventsPerRun so the display window, not the
// store, is the usual eviction point.
const defaultTranscriptRing = 2000

// transcriptPollLimit caps one poll pass, so a large backlog arrives over
// several passes instead of one unbounded read.
const transcriptPollLimit = 500

// defaultTranscriptHeight is the scrollback viewport height when none is set.
const defaultTranscriptHeight = 20

// noCursor is the pre-attach cursor: one below the first possible Seq (0).
const noCursor = int64(-1)

// TranscriptTailer polls a TranscriptStore for new events and appends them
// to a capped RingBuffer. It tracks one cursor (the last Seq seen) per tailed
// AgentRun, so each Poll reads only new events, and it holds a scrollback
// offset into the retained window. Several attempts for one Issue share one
// tailer through AddRun, which makes the retry history one scrollback.
type TranscriptTailer struct {
	store TranscriptStore
	// runs holds every tailed AgentRun in insertion order, so a retry extends
	// the same scrollback and the pane numbers the attempts from the order.
	runs []*runTail
	ring *RingBuffer

	// height is the scrollback viewport height in events.
	height int
	// offset counts events scrolled back from the tail. Zero follows the tail.
	offset int

	// evicted records that earlier events are not retained, from store-side
	// retention or ring eviction. Distinct from a TRUNCATION marker: storage
	// never persists TRUNCATION, so a leading seq gap means eviction.
	evicted bool
}

// NewTranscriptTailer builds a tailer over store for agentRunID with a ring
// buffer of the given cap. A cap of zero or less uses defaultTranscriptRing.
func NewTranscriptTailer(store TranscriptStore, agentRunID int64, cap int) *TranscriptTailer {
	if cap <= 0 {
		cap = defaultTranscriptRing
	}
	return &TranscriptTailer{
		store:  store,
		runs:   []*runTail{{id: agentRunID, cursor: noCursor}},
		ring:   NewRingBuffer(cap),
		height: defaultTranscriptHeight,
	}
}

// runTail is one tailed AgentRun (attempt) and its own read cursor.
type runTail struct {
	id int64
	// cursor is the last Seq read from this run. The tail API reads seq
	// strictly greater than the cursor and the first event of a run carries
	// Seq 0, so an unattached run holds noCursor, not 0.
	cursor int64
	// attached records that one poll has completed for this run, so the first
	// pass can detect history the store evicted before the reader attached.
	attached bool
}

// AddRun appends a retry's AgentRun to the tailed history. The retained window
// keeps every earlier attempt, so the pane reads one continuous scrollback with
// an attempt divider at each boundary. A known run changes nothing.
func (t *TranscriptTailer) AddRun(agentRunID int64) {
	for _, r := range t.runs {
		if r.id == agentRunID {
			return
		}
	}
	t.runs = append(t.runs, &runTail{id: agentRunID, cursor: noCursor})
}

// RunOrder returns every tailed AgentRun in insertion order.
func (t *TranscriptTailer) RunOrder() []int64 {
	order := make([]int64, 0, len(t.runs))
	for _, r := range t.runs {
		order = append(order, r.id)
	}
	return order
}

// SetHeight sets the scrollback viewport height in events. A height of zero
// or less restores the default.
func (t *TranscriptTailer) SetHeight(h int) {
	if h <= 0 {
		h = defaultTranscriptHeight
	}
	t.height = h
}

// Poll fetches new events for every tailed attempt and appends them to the ring
// buffer. Every pass reads every attempt, oldest first. A read failure stops the
// pass but keeps the attempts it already read, and the next pass starts again at
// the oldest attempt, which the untouched cursors make cheap.
func (t *TranscriptTailer) Poll(ctx context.Context) (TranscriptViewModel, error) {
	read, err := t.fetch(ctx, nil)
	return t.apply(read), err
}

// transcriptRead holds one fetch pass's events, keyed by attempt. apply commits
// it. It is a token to carry between the two halves, never a view input.
type transcriptRead struct {
	runs []runEvents
}

// runEvents is one attempt's newly read events, oldest first.
type runEvents struct {
	id     int64
	events []storage.TranscriptEvent
}

// fetch reads new events for every attempt in runIDs, oldest attempt first, and
// changes no tailer state. apply commits the result. The split exists so the
// store reads can run on another goroutine while the tailer's owner keeps
// serving key presses. An empty runIDs reads the attempts the tailer already
// holds. An unknown run reads from its retained start. A read failure returns
// the attempts it already read with the error.
func (t *TranscriptTailer) fetch(ctx context.Context, runIDs []int64) (transcriptRead, error) {
	if len(runIDs) == 0 {
		runIDs = t.RunOrder()
	}
	var read transcriptRead
	for _, id := range runIDs {
		events, err := t.store.TranscriptEventsAfter(ctx, id, t.cursorOf(id), transcriptPollLimit)
		if err != nil {
			return read, fmt.Errorf("tui: poll transcript for run %d: %w", id, err)
		}
		read.runs = append(read.runs, runEvents{id: id, events: events})
	}
	return read, nil
}

// apply appends a fetched pass to the ring buffer, adds any attempt the pass
// names that the tailer does not hold, and returns the window. A partial pass
// commits what it read.
func (t *TranscriptTailer) apply(read transcriptRead) TranscriptViewModel {
	anchor, anchored := t.anchor(t.orderedWindow())
	for _, rd := range read.runs {
		t.AddRun(rd.id)
		t.ingest(rd.id, rd.events)
	}
	return t.commit(anchor, anchored)
}

// runOf returns the tailed attempt with id, or nil.
func (t *TranscriptTailer) runOf(id int64) *runTail {
	for _, r := range t.runs {
		if r.id == id {
			return r
		}
	}
	return nil
}

// cursorOf returns the last Seq read from attempt id. An untailed attempt holds
// noCursor, so it reads from its retained start.
func (t *TranscriptTailer) cursorOf(id int64) int64 {
	if r := t.runOf(id); r != nil {
		return r.cursor
	}
	return noCursor
}

// anchor returns the key of the first visible event in retained while the
// viewport is scrolled back. A tail-following viewport needs no anchor.
func (t *TranscriptTailer) anchor(retained []TranscriptEvent) (eventKey, bool) {
	if t.offset <= 0 || len(retained) == 0 {
		return eventKey{}, false
	}
	start, _ := t.windowBounds(len(retained))
	return keyOf(retained[start]), true
}

// commit records eviction and holds the scrollback anchor as the tail advances.
// It re-derives the offset from the anchor's place in the ordered window, so an
// event that sorts in ahead of the window does not slide it. A partial pass
// commits as well. An evicted anchor uses its retained successor.
func (t *TranscriptTailer) commit(anchor eventKey, anchored bool) TranscriptViewModel {
	if t.ring.Dropped() > 0 {
		t.evicted = true
	}
	retained := t.orderedWindow()
	if anchored {
		at := indexOfEvent(retained, anchor)
		if at < 0 {
			at = t.indexOfSuccessor(retained, anchor)
		}
		if at >= 0 {
			t.offset = t.offsetForStart(len(retained), at)
		}
		t.clampOffset()
	}
	return t.snapshotFrom(retained)
}

// indexOfEvent returns the position of key in events, or -1.
func indexOfEvent(events []TranscriptEvent, key eventKey) int {
	for i, e := range events {
		if keyOf(e) == key {
			return i
		}
	}
	return -1
}

// indexOfSuccessor returns the first retained event after key in view order.
func (t *TranscriptTailer) indexOfSuccessor(events []TranscriptEvent, key eventKey) int {
	rank := t.runRanks()
	keyRank := rank[key.runID]
	for i, e := range events {
		eventRank := rank[e.AgentRunID]
		if eventRank > keyRank || (eventRank == keyRank && e.Seq > key.seq) {
			return i
		}
	}
	return -1
}

// ingest appends one attempt's newly read events to the ring and advances its
// cursor. An attempt the tailer does not hold is ignored, so Apply adds it first.
func (t *TranscriptTailer) ingest(id int64, events []storage.TranscriptEvent) {
	r := t.runOf(id)
	if r == nil {
		return
	}

	// The first pass backfills from the retained start. A first event past
	// seq 0 means the store's retention window already dropped history.
	if !r.attached {
		r.attached = true
		if len(events) > 0 && events[0].Seq > 0 {
			t.evicted = true
		}
	}

	for _, e := range events {
		ev := convertTranscriptEvent(e)
		if ev.AgentRunID == 0 {
			// Every event must name its run, because the divider and the
			// selection key both read it. The read scope names the run, so fill
			// it in rather than trust the column.
			ev.AgentRunID = r.id
		}
		t.ring.Append(ev)
		if int64(e.Seq) > r.cursor {
			r.cursor = int64(e.Seq)
		}
	}
}

// ScrollUp moves the viewport n events towards the retained start.
func (t *TranscriptTailer) ScrollUp(n int) {
	t.offset += n
	t.clampOffset()
}

// ScrollDown moves the viewport n events towards the tail.
func (t *TranscriptTailer) ScrollDown(n int) {
	t.offset -= n
	t.clampOffset()
}

// ScrollToTail returns the viewport to following the tail.
func (t *TranscriptTailer) ScrollToTail() { t.offset = 0 }

// PageSize returns the viewport height in events.
func (t *TranscriptTailer) PageSize() int { return t.height }

// clampOffset keeps the scrollback offset inside the retained window.
func (t *TranscriptTailer) clampOffset() {
	max := t.ring.Len() - t.height
	if max < 0 {
		max = 0
	}
	if t.offset > max {
		t.offset = max
	}
	if t.offset < 0 {
		t.offset = 0
	}
}

// Reattach replaces the tailed runs with agentRunID alone and clears the
// retained window, so the next Poll backfills from the store's retained start. Use it
// after a reader gap. Use AddRun for a retry, which keeps the earlier attempts.
func (t *TranscriptTailer) Reattach(agentRunID int64) {
	t.runs = []*runTail{{id: agentRunID, cursor: noCursor}}
	t.Reset()
}

// Reset empties the ring buffer and resets every cursor, the scrollback, and
// the eviction marker. The tailed runs stay.
func (t *TranscriptTailer) Reset() {
	t.ring.Reset()
	for _, r := range t.runs {
		r.cursor = noCursor
		r.attached = false
	}
	t.offset = 0
	t.evicted = false
}

// LatestRunCursor returns the last Seq read from the newest attempt alone. It
// is not a position in the rendered scrollback, which spans every attempt.
func (t *TranscriptTailer) LatestRunCursor() int64 {
	if len(t.runs) == 0 {
		return noCursor
	}
	return t.runs[len(t.runs)-1].cursor
}

// snapshot builds a TranscriptViewModel from the ring's current state,
// windowed by the scrollback offset and height.
func (t *TranscriptTailer) snapshot() TranscriptViewModel {
	return t.snapshotFrom(t.orderedWindow())
}

// windowBounds turns the scrollback offset into the visible range of a retained
// window of n events. It is the one place that holds the relation between the
// offset and the window, and offsetForStart is its inverse.
func (t *TranscriptTailer) windowBounds(n int) (start, end int) {
	end = n - t.offset
	if end < 0 {
		end = 0
	}
	start = end - t.height
	if start < 0 {
		start = 0
	}
	return start, end
}

// offsetForStart returns the offset that puts start at the top of the visible
// window. It inverts windowBounds, so a held anchor keeps its place as the tail
// advances.
func (t *TranscriptTailer) offsetForStart(n, start int) int {
	return n - (start + t.height)
}

// snapshotFrom builds the view model from an already ordered window, so one Poll
// orders the events once.
func (t *TranscriptTailer) snapshotFrom(retained []TranscriptEvent) TranscriptViewModel {
	start, end := t.windowBounds(len(retained))
	window := retained[start:end]
	vm := TranscriptViewModel{
		Events:   window,
		Evicted:  t.evicted,
		Dropped:  t.ring.Dropped(),
		AtTail:   t.offset == 0,
		AtStart:  start == 0,
		Retained: len(retained),
		RunOrder: t.RunOrder(),
	}
	if len(window) > 0 {
		vm.FirstSeq = window[0].Seq
		vm.FirstRunID = window[0].AgentRunID
	}
	return vm
}

// orderedWindow returns the retained events in run insertion order, then seq.
// Ring order is append order, so a late event from an earlier attempt lands at
// the tail; the sort keeps each attempt contiguous, which the divider needs.
func (t *TranscriptTailer) orderedWindow() []TranscriptEvent {
	retained := t.ring.Window()
	rank := t.runRanks()
	sort.SliceStable(retained, func(i, j int) bool {
		if rank[retained[i].AgentRunID] != rank[retained[j].AgentRunID] {
			return rank[retained[i].AgentRunID] < rank[retained[j].AgentRunID]
		}
		return retained[i].Seq < retained[j].Seq
	})
	return retained
}

// runRanks maps each tailed attempt to its view order rank.
func (t *TranscriptTailer) runRanks() map[int64]int {
	rank := make(map[int64]int, len(t.runs))
	for i, r := range t.runs {
		rank[r.id] = i
	}
	return rank
}

// convertTranscriptEvent narrows a stored event to the fields the TUI shows.
func convertTranscriptEvent(e storage.TranscriptEvent) TranscriptEvent {
	return TranscriptEvent{
		AgentRunID: e.AgentRunID,
		Seq:        e.Seq,
		Type:       e.Type,
		Role:       e.Role,
		Text:       e.Text,
		ToolName:   e.ToolName,
		ToolInput:  e.ToolInput,
		ToolOutput: e.ToolOutput,
		ToolCallID: e.ToolCallID,
		Subagent:   e.Subagent,
		OccurredAt: e.OccurredAt,
	}
}

// TranscriptViewModel is the plain, transportable input to the transcript
// pane's renderer.
type TranscriptViewModel struct {
	// Events is the visible window, oldest first.
	Events []TranscriptEvent
	// Evicted marks that earlier events are not retained. The pane renders
	// it with wording distinct from a TRUNCATION marker.
	Evicted bool
	// Dropped counts events the ring front-evicted.
	Dropped int
	// AtTail reports that the window follows the live tail.
	AtTail bool
	// AtStart reports that the window begins at the retained start, so no
	// scrollback remains. The pane needs it to refuse an inert scroll.
	AtStart bool
	// Retained counts every event held in the ring, visible or scrolled off.
	Retained int
	// RunOrder lists every tailed AgentRun in insertion order. The pane reads
	// the attempt numbers from it, so the divider numbers stay stable while the
	// visible window moves. It must name every retained event's run.
	RunOrder []int64
	// FirstSeq is the Seq of the first event in Events, the window's leading
	// entry. The pane reads it, with FirstRunID, to size a scrollback request
	// that recovers a pinned entry the tail pushed several events out of the
	// window in one poll. Zero on an empty window.
	FirstSeq int
	// FirstRunID is the AgentRun of the first event in Events. It scopes
	// FirstSeq to the right attempt, since Seq restarts at 0 on each run.
	FirstRunID int64
}

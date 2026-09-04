package tui

// Package tui/transcript.go implements the live transcript tailing pipeline:
// a poller over the Store's bounded tail API that feeds a capped ring buffer
// and a scrollback window. The read path is wholly separate from the capture
// path, so a wedged or slow reader can never stall an Execution (ADR 0030):
// the worst it produces is a gap the view marks.

import (
	"context"
	"fmt"

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
// to a capped RingBuffer. It tracks a cursor (the last Seq seen) so each Poll
// reads only new events, and it holds a scrollback offset into the retained
// window.
type TranscriptTailer struct {
	store TranscriptStore
	runID int64
	// cursor is the last Seq read. The tail API reads seq strictly greater
	// than the cursor and the first event of a run carries Seq 0, so an
	// unattached tailer holds noCursor, not 0.
	cursor int64
	ring   *RingBuffer

	// height is the scrollback viewport height in events.
	height int
	// offset counts events scrolled back from the tail. Zero follows the tail.
	offset int

	// attached records that one poll has completed, so the first pass can
	// detect history the store evicted before the reader attached.
	attached bool
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
		runID:  agentRunID,
		ring:   NewRingBuffer(cap),
		cursor: noCursor,
		height: defaultTranscriptHeight,
	}
}

// SetHeight sets the scrollback viewport height in events. A height of zero
// or less restores the default.
func (t *TranscriptTailer) SetHeight(h int) {
	if h <= 0 {
		h = defaultTranscriptHeight
	}
	t.height = h
}

// Poll fetches events after the cursor and appends them to the ring buffer.
// A read failure leaves the cursor and the retained window untouched, so the
// next pass resumes from the same point.
func (t *TranscriptTailer) Poll(ctx context.Context) (TranscriptViewModel, error) {
	events, err := t.store.TranscriptEventsAfter(ctx, t.runID, t.cursor, transcriptPollLimit)
	if err != nil {
		return t.snapshot(), fmt.Errorf("tui: poll transcript for run %d: %w", t.runID, err)
	}

	// The first pass backfills from the retained start. A first event past
	// seq 0 means the store's retention window already dropped history.
	if !t.attached {
		t.attached = true
		if len(events) > 0 && events[0].Seq > 0 {
			t.evicted = true
		}
	}

	for _, e := range events {
		t.ring.Append(convertTranscriptEvent(e))
		if int64(e.Seq) > t.cursor {
			t.cursor = int64(e.Seq)
		}
	}
	if t.ring.Dropped() > 0 {
		t.evicted = true
	}
	if t.offset > 0 {
		// Hold the scrollback anchor as the tail advances.
		t.offset += len(events)
		t.clampOffset()
	}
	return t.snapshot(), nil
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

// Reattach clears the retained window and the cursor so the next Poll
// backfills from the store's retained start. Use it after a reader gap or a
// new AgentRun for the same tailer.
func (t *TranscriptTailer) Reattach(agentRunID int64) {
	t.runID = agentRunID
	t.Reset()
}

// Reset empties the ring buffer and resets the cursor, scrollback, and
// eviction marker.
func (t *TranscriptTailer) Reset() {
	t.ring.Reset()
	t.cursor = noCursor
	t.offset = 0
	t.attached = false
	t.evicted = false
}

// Cursor returns the last Seq the tailer has read.
func (t *TranscriptTailer) Cursor() int64 { return t.cursor }

// snapshot builds a TranscriptViewModel from the ring's current state,
// windowed by the scrollback offset and height.
func (t *TranscriptTailer) snapshot() TranscriptViewModel {
	retained := t.ring.Window()
	end := len(retained) - t.offset
	if end < 0 {
		end = 0
	}
	start := end - t.height
	if start < 0 {
		start = 0
	}
	return TranscriptViewModel{
		Events:   retained[start:end],
		Evicted:  t.evicted,
		Dropped:  t.ring.Dropped(),
		AtTail:   t.offset == 0,
		Retained: len(retained),
	}
}

// convertTranscriptEvent narrows a stored event to the fields the TUI shows.
func convertTranscriptEvent(e storage.TranscriptEvent) TranscriptEvent {
	return TranscriptEvent{
		Seq:        e.Seq,
		Type:       e.Type,
		Role:       e.Role,
		Text:       e.Text,
		ToolName:   e.ToolName,
		ToolInput:  e.ToolInput,
		ToolOutput: e.ToolOutput,
		ToolCallID: e.ToolCallID,
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
	// Retained counts every event held in the ring, visible or scrolled off.
	Retained int
}

package tui

import "time"

// RingBuffer is a capped FIFO of TranscriptEvents keyed by stable Seq. Once
// the cap is exceeded the oldest retained event is dropped, producing a seq
// gap the reader can detect. A zero cap means unbounded.
type RingBuffer struct {
	buf     []TranscriptEvent
	cap     int
	dropped int
}

// TranscriptEvent is the TUI's own event type: a subset of
// storage.TranscriptEvent the tailer needs for display. Kept narrow so the
// TUI package has no dependency on storage internals.
type TranscriptEvent struct {
	// AgentRunID names the attempt the event belongs to. Seq restarts at 0 in
	// every run, so only the pair identifies an event across a retry history.
	AgentRunID int64
	Seq        int
	Type       string
	Role       string
	Text       string
	ToolName   string
	ToolInput  string
	ToolOutput string
	ToolCallID string
	// Subagent names the subagent that produced the event (a review axis such
	// as "bugs", "quality", "docs"), read straight off
	// transcript_events.subagent. Empty for the single implementation Agent,
	// which needs no label.
	Subagent string
	// OccurredAt is the event's own recorded time. The pane compares it
	// against a gate row's FinishedAt, so a gate that ran early interleaves
	// into the timeline instead of trailing every event.
	OccurredAt time.Time
}

// NewRingBuffer returns an empty RingBuffer that retains at most cap events.
// A zero cap means unbounded (no eviction).
func NewRingBuffer(cap int) *RingBuffer {
	return &RingBuffer{cap: cap}
}

// Append adds one event to the ring, evicting the oldest when over cap.
func (r *RingBuffer) Append(e TranscriptEvent) {
	r.buf = append(r.buf, e)
	if r.cap > 0 && len(r.buf) > r.cap {
		r.buf = r.buf[1:]
		r.dropped++
	}
}

// Window returns every retained event in insertion order. The returned slice
// is a fresh copy.
func (r *RingBuffer) Window() []TranscriptEvent {
	if len(r.buf) == 0 {
		return nil
	}
	out := make([]TranscriptEvent, len(r.buf))
	copy(out, r.buf)
	return out
}

// Len returns how many events the ring retains.
func (r *RingBuffer) Len() int { return len(r.buf) }

// Dropped returns how many events were front-evicted.
func (r *RingBuffer) Dropped() int { return r.dropped }

// HeadSeq returns the Seq of the oldest retained event.
func (r *RingBuffer) HeadSeq() int {
	if len(r.buf) == 0 {
		return 0
	}
	return r.buf[0].Seq
}

// TailSeq returns the Seq of the most recently appended event.
func (r *RingBuffer) TailSeq() int {
	if len(r.buf) == 0 {
		return 0
	}
	return r.buf[len(r.buf)-1].Seq
}

// Reset empties the buffer and clears the dropped counter.
func (r *RingBuffer) Reset() {
	r.buf = r.buf[:0]
	r.dropped = 0
}

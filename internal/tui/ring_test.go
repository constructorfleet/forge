package tui_test

import (
	"testing"

	"github.com/Teagan42/forge/internal/tui"
)

// TestRingBuffer_AppendAndWindow proves the ring retains the last cap events
// and exposes them as the scrollback window in insertion order.
func TestRingBuffer_AppendAndWindow(t *testing.T) {
	r := tui.NewRingBuffer(3)
	events(0, 1, 2).append(r)

	win := r.Window()
	assertSeqs(t, win, 0, 1, 2)
}

// TestRingBuffer_FrontEvicts proves appending past cap drops the oldest.
func TestRingBuffer_FrontEvicts(t *testing.T) {
	r := tui.NewRingBuffer(3)
	events(0, 1, 2, 3).append(r) // evicts seq 0

	win := r.Window()
	assertSeqs(t, win, 1, 2, 3)
}

// TestRingBuffer_DroppedCount proves Dropped reports how many were evicted.
func TestRingBuffer_DroppedCount(t *testing.T) {
	r := tui.NewRingBuffer(2)
	if r.Dropped() != 0 {
		t.Fatalf("Dropped() = %d, want 0", r.Dropped())
	}
	events(0, 1).append(r)
	if r.Dropped() != 0 {
		t.Fatalf("Dropped() = %d after filling, want 0", r.Dropped())
	}
	events(2, 3).append(r) // evicts 0, 1
	if r.Dropped() != 2 {
		t.Fatalf("Dropped() = %d, want 2", r.Dropped())
	}
}

// TestRingBuffer_EmptyWindow proves Window returns nil on an empty buffer.
func TestRingBuffer_EmptyWindow(t *testing.T) {
	r := tui.NewRingBuffer(5)
	if win := r.Window(); win != nil {
		t.Fatalf("Window() = %v, want nil", win)
	}
}

// TestRingBuffer_ResetClears proves Reset empties the buffer.
func TestRingBuffer_ResetClears(t *testing.T) {
	r := tui.NewRingBuffer(2)
	events(0, 1, 2).append(r) // evicts 0
	if r.Dropped() != 1 {
		t.Fatalf("pre-Reset Dropped() = %d, want 1", r.Dropped())
	}
	r.Reset()
	if r.Dropped() != 0 {
		t.Fatalf("post-Reset Dropped() = %d, want 0", r.Dropped())
	}
	if win := r.Window(); win != nil {
		t.Fatalf("post-Reset Window() = %v, want nil", win)
	}
}

// TestRingBuffer_HeadSeqAndTailSeq prove the cursor bounds of retained data.
func TestRingBuffer_HeadSeqAndTailSeq(t *testing.T) {
	r := tui.NewRingBuffer(3)
	events(0, 1, 2).append(r)
	if got := r.HeadSeq(); got != 0 {
		t.Fatalf("HeadSeq() = %d, want 0", got)
	}
	if got := r.TailSeq(); got != 2 {
		t.Fatalf("TailSeq() = %d, want 2", got)
	}
	events(3).append(r) // evicts 0
	if got := r.HeadSeq(); got != 1 {
		t.Fatalf("HeadSeq() after eviction = %d, want 1", got)
	}
	if got := r.TailSeq(); got != 3 {
		t.Fatalf("TailSeq() after eviction = %d, want 3", got)
	}
}

// helper: build a slice of TranscriptEvents with the given seqs.
type eventSeqs []int

func events(seqs ...int) eventSeqs { return eventSeqs(seqs) }

func (es eventSeqs) append(r *tui.RingBuffer) {
	for _, s := range es {
		r.Append(tui.TranscriptEvent{Seq: s})
	}
}

func assertSeqs(t *testing.T, got []tui.TranscriptEvent, want ...int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, e := range got {
		if e.Seq != want[i] {
			t.Fatalf("[%d] Seq = %d, want %d", i, e.Seq, want[i])
		}
	}
}

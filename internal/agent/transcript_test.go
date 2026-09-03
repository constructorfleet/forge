package agent_test

import (
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
)

// TestTranscriptRecorder_AssignsStableSeqAtEmit verifies Seq is a stable
// per-run arrival ordinal assigned once at Emit (ADR 0030) and never
// renumbered on read: Events() returns the exact seqs assigned at emit.
func TestTranscriptRecorder_AssignsStableSeqAtEmit(t *testing.T) {
	r := agent.NewTranscriptRecorder()
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "first"})
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventToolCall, ToolName: "Bash"})
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventToolResult, ToolOutput: "ok"})

	events := r.Events()
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	for i, event := range events {
		if event.Seq != i {
			t.Fatalf("events[%d].Seq = %d, want %d", i, event.Seq, i)
		}
	}
	if events[0].Text != "first" || events[1].ToolName != "Bash" || events[2].ToolOutput != "ok" {
		t.Fatalf("events = %+v, want fields preserved from Emit", events)
	}
}

func TestTranscriptRecorder_EventsReturnsIndependentCopy(t *testing.T) {
	r := agent.NewTranscriptRecorder()
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "first"})

	snapshot := r.Events()
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "second"})

	if len(snapshot) != 1 {
		t.Fatalf("earlier snapshot mutated: got %d events, want 1", len(snapshot))
	}
	if len(r.Events()) != 2 {
		t.Fatalf("got %d events after second Emit, want 2", len(r.Events()))
	}
}

func TestTranscriptRecorder_EmptyRecorderReturnsNoEvents(t *testing.T) {
	r := agent.NewTranscriptRecorder()
	if events := r.Events(); len(events) != 0 {
		t.Fatalf("got %d events, want 0", len(events))
	}
}

// TestTranscriptRecorder_EmitStampsTimestampWhenZero is issue 36's
// per-event-timestamp requirement for emitters (like clicommon/openai) that
// have no real per-event backend clock of their own: the recorder's clock
// fills in a real, distinct, monotonic time for each event rather than
// leaving it at the zero value.
func TestTranscriptRecorder_EmitStampsTimestampWhenZero(t *testing.T) {
	base := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	step := 0
	r := agent.NewBoundedTranscriptRecorder(0, func() time.Time {
		step++
		return base.Add(time.Duration(step) * time.Millisecond)
	})
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "first"})
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "second"})
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "third"})

	events := r.Events()
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	seen := map[time.Time]bool{}
	for i, event := range events {
		if event.Timestamp.IsZero() {
			t.Fatalf("events[%d].Timestamp is zero, want the recorder's clock to have stamped it", i)
		}
		if seen[event.Timestamp] {
			t.Fatalf("events[%d].Timestamp = %v duplicates an earlier event's timestamp, want distinct", i, event.Timestamp)
		}
		seen[event.Timestamp] = true
		if i > 0 && !event.Timestamp.After(events[i-1].Timestamp) {
			t.Fatalf("events[%d].Timestamp = %v, want strictly after events[%d].Timestamp = %v (monotonic)", i, event.Timestamp, i-1, events[i-1].Timestamp)
		}
	}
}

// TestTranscriptRecorder_PreservesCallerSuppliedTimestamp verifies an
// emitter with its own real per-event clock (the Claude adapter's
// stream-json timestamps) is trusted as-is rather than overwritten by the
// recorder's clock.
func TestTranscriptRecorder_PreservesCallerSuppliedTimestamp(t *testing.T) {
	want := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	r := agent.NewBoundedTranscriptRecorder(0, func() time.Time { return time.Now() })
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "first", Timestamp: want})

	events := r.Events()
	if len(events) != 1 || !events[0].Timestamp.Equal(want) {
		t.Fatalf("events = %+v, want Timestamp preserved as %v", events, want)
	}
}

// TestTranscriptRecorder_StableSeqLeavesEvictionGaps verifies the ADR 0030
// stable-seq contract under a bounded recorder: when events are evicted the
// retained events keep their original emit-time seqs (never renumbered), so
// the reader sees a gap where eviction happened rather than a dense
// re-sequenced window. No synthetic TRUNCATION row is synthesized: storage
// no longer persists it (the drop is implicit in the gap).
func TestTranscriptRecorder_StableSeqLeavesEvictionGaps(t *testing.T) {
	r := agent.NewBoundedTranscriptRecorder(2, func() time.Time { return time.Now() })
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "one"})
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "two"})
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "three"})
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "four"})

	events := r.Events()
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 retained: %+v", len(events), events)
	}
	// The two most recent events keep their original stable seqs (2, 3),
	// leaving an eviction gap at seqs 0 and 1.
	if events[0].Text != "three" || events[0].Seq != 2 {
		t.Fatalf("events[0] = %+v, want 'three' at stable seq 2", events[0])
	}
	if events[1].Text != "four" || events[1].Seq != 3 {
		t.Fatalf("events[1] = %+v, want 'four' at stable seq 3", events[1])
	}
	if events[0].Type == agent.TranscriptEventTruncation {
		t.Fatalf("events[0] = %+v, want no synthesized TRUNCATION marker", events[0])
	}
}

// TestTranscriptRecorder_EmitCountReportsDropFloor verifies the recorder
// reports how many events were emitted versus how many are retained, so a
// caller (the sink) knows the lowest retained seq — the eviction floor — and
// that eviction happened, without a synthetic TRUNCATION row.
func TestTranscriptRecorder_EmitCountReportsDropFloor(t *testing.T) {
	r := agent.NewBoundedTranscriptRecorder(2, func() time.Time { return time.Now() })
	for _, text := range []string{"a", "b", "c", "d", "e"} {
		r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: text})
	}
	if got := r.Emitted(); got != 5 {
		t.Fatalf("Emitted() = %d, want 5", got)
	}
	if got := r.FirstSeq(); got != 3 {
		t.Fatalf("FirstSeq() = %d, want 3 (eviction floor: seqs 0,1,2 dropped)", got)
	}
	if got := len(r.Events()); got != 2 {
		t.Fatalf("len(Events()) = %d, want 2 retained", got)
	}
}

// TestTranscriptRecorder_UnboundedRecorderNeverTruncates confirms max=0
// (NewTranscriptRecorder's default) keeps every event, so existing callers
// that never opted into a bound are unaffected.
func TestTranscriptRecorder_UnboundedRecorderNeverTruncates(t *testing.T) {
	r := agent.NewTranscriptRecorder()
	for i := 0; i < 10; i++ {
		r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "event"})
	}
	events := r.Events()
	if len(events) != 10 {
		t.Fatalf("got %d events, want 10 (unbounded)", len(events))
	}
}

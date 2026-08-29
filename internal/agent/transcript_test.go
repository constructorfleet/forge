package agent_test

import (
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
)

func TestTranscriptRecorder_AssignsSeqInArrivalOrder(t *testing.T) {
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

// TestTranscriptRecorder_BoundedRecorderDropsOldestAndMarksTruncation is
// issue 36's bounded-truncation requirement: exceeding max must not
// silently keep an unlabelled sliver — it evicts the oldest event and
// Events() must lead with an explicit TranscriptEventTruncation marker
// reporting the drop count, followed by the most-recent max events in
// order.
func TestTranscriptRecorder_BoundedRecorderDropsOldestAndMarksTruncation(t *testing.T) {
	r := agent.NewBoundedTranscriptRecorder(2, func() time.Time { return time.Now() })
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "one"})
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "two"})
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "three"})
	r.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Text: "four"})

	events := r.Events()
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3 (1 truncation marker + 2 retained): %+v", len(events), events)
	}
	if events[0].Type != agent.TranscriptEventTruncation || events[0].Seq != 0 {
		t.Fatalf("events[0] = %+v, want a leading TranscriptEventTruncation marker at Seq 0", events[0])
	}
	if events[1].Text != "three" || events[1].Seq != 1 {
		t.Fatalf("events[1] = %+v, want the third emitted event at Seq 1", events[1])
	}
	if events[2].Text != "four" || events[2].Seq != 2 {
		t.Fatalf("events[2] = %+v, want the fourth emitted event at Seq 2", events[2])
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
	if events[0].Type == agent.TranscriptEventTruncation {
		t.Fatalf("events[0] = %+v, want no truncation marker for an unbounded recorder", events[0])
	}
}

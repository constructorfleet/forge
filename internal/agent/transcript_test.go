package agent_test

import (
	"testing"

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

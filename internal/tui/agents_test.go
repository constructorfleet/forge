package tui_test

import (
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
)

// TestDeriveAgentsAlwaysLeadsWithImplementation proves the agents pane lists
// the implementation Agent first even before it records an event, so the row
// the operator lands on never vanishes between polls.
func TestDeriveAgentsAlwaysLeadsWithImplementation(t *testing.T) {
	got := tui.DeriveAgents(nil)
	if len(got) != 1 || got[0].Label != "implementation" || got[0].Subagent != "" {
		t.Fatalf("DeriveAgents(nil) = %+v, want one implementation row", got)
	}
}

// TestDeriveAgentsOneRowPerReviewSubagent proves each review subagent gets
// its own row after the implementation row, in first-seen order, with the
// review retries of one subagent merged into one row.
func TestDeriveAgentsOneRowPerReviewSubagent(t *testing.T) {
	at := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	agents := []storage.TranscriptAgent{
		{AgentRunID: 1, Phase: "IMPLEMENTING", Events: 4, Last: storage.TranscriptEvent{Type: "TOOL_CALL", ToolName: "Bash", OccurredAt: at}},
		{AgentRunID: 2, Phase: "REVIEWING", Subagent: "bugs", Events: 2, Last: storage.TranscriptEvent{Type: "MESSAGE", Text: "No bugs found.\nDetails follow.", OccurredAt: at.Add(2 * time.Second)}},
		{AgentRunID: 3, Phase: "REVIEWING", Subagent: "docs", Events: 1, Last: storage.TranscriptEvent{Type: "MESSAGE", Text: "Docs ok.", OccurredAt: at.Add(3 * time.Second)}},
		{AgentRunID: 4, Phase: "IMPLEMENTING", Events: 3, Last: storage.TranscriptEvent{Type: "MESSAGE", Text: "Fixed the finding.", OccurredAt: at.Add(4 * time.Second)}},
		{AgentRunID: 5, Phase: "REVIEWING", Subagent: "bugs", Events: 5, Last: storage.TranscriptEvent{Type: "TOOL_RESULT", ToolName: "Read", ToolOutput: "package tui", OccurredAt: at.Add(5 * time.Second)}},
	}
	got := tui.DeriveAgents(agents)
	want := []tui.AgentRow{
		{Label: "implementation", Phase: "IMPLEMENTING", Subagent: "", Events: 7, Latest: "Fixed the finding.", LastAt: at.Add(4 * time.Second)},
		{Label: "review: bugs", Phase: "REVIEWING", Subagent: "bugs", Events: 7, Latest: "└ Read: package tui", LastAt: at.Add(5 * time.Second)},
		{Label: "review: docs", Phase: "REVIEWING", Subagent: "docs", Events: 1, Latest: "Docs ok.", LastAt: at.Add(3 * time.Second)},
	}
	if len(got) != len(want) {
		t.Fatalf("DeriveAgents returned %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestSummarizeEventOneLinePerKind proves the latest-output column reads one
// line per event kind: a tool call by name, a tool result by name and first
// output line, a message by its first line, a truncation by a marker.
func TestSummarizeEventOneLinePerKind(t *testing.T) {
	cases := []struct {
		name string
		e    storage.TranscriptEvent
		want string
	}{
		{"tool call", storage.TranscriptEvent{Type: "TOOL_CALL", ToolName: "Bash", ToolInput: "go test"}, "▸ Bash"},
		{"tool result", storage.TranscriptEvent{Type: "TOOL_RESULT", ToolName: "Bash", ToolOutput: "ok\nmore"}, "└ Bash: ok"},
		{"message", storage.TranscriptEvent{Type: "MESSAGE", Text: "first\nsecond"}, "first"},
		{"thinking", storage.TranscriptEvent{Type: "MESSAGE", Role: "thinking", Text: "hmm"}, "∴ hmm"},
		{"truncation", storage.TranscriptEvent{Type: "TRUNCATION", Text: "dropped 3"}, "░ dropped 3"},
		{"empty", storage.TranscriptEvent{}, ""},
	}
	for _, tc := range cases {
		if got := tui.SummarizeEvent(tc.e); got != tc.want {
			t.Errorf("%s: SummarizeEvent = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestWorkerRowLatestOutputPicksTheNewestAgent proves the execution list's
// latest-output column follows whichever agent spoke last, not the first row.
func TestWorkerRowLatestOutputPicksTheNewestAgent(t *testing.T) {
	at := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	row := tui.WorkerRow{Agents: []tui.AgentRow{
		{Label: "implementation", Latest: "older", LastAt: at},
		{Label: "review: bugs", Latest: "newest", LastAt: at.Add(time.Second)},
		{Label: "review: docs", Latest: "middle", LastAt: at.Add(time.Millisecond)},
	}}
	if got := row.LatestOutput(); got != "newest" {
		t.Fatalf("LatestOutput = %q, want %q", got, "newest")
	}
	if got := row.AgentCount(); got != 3 {
		t.Fatalf("AgentCount = %d, want 3", got)
	}
	if got := (tui.WorkerRow{}).LatestOutput(); got != "" {
		t.Fatalf("LatestOutput on an empty row = %q, want empty", got)
	}
}

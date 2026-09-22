package tui_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
)

// agentFilterFixture holds an implementation run and one review run whose
// events interleave, plus a passed gate, so the tests can prove the filter
// separates the two agents and keeps the gate with the implementation.
func agentFilterFixture() *fakeFeedStore {
	at := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	return &fakeFeedStore{
		runs: map[string][]storage.AgentRun{"#1": {{ID: 1}, {ID: 2}}},
		events: map[int64][]storage.TranscriptEvent{
			1: {
				{AgentRunID: 1, Seq: 0, Type: "MESSAGE", Text: "implementing", Phase: "IMPLEMENTING", OccurredAt: at},
				{AgentRunID: 1, Seq: 1, Type: "MESSAGE", Text: "implemented", Phase: "IMPLEMENTING", OccurredAt: at.Add(time.Second)},
			},
			2: {
				{AgentRunID: 2, Seq: 0, Type: "MESSAGE", Text: "reviewing bugs", Phase: "REVIEWING", Subagent: "bugs", OccurredAt: at.Add(2 * time.Second)},
				{AgentRunID: 2, Seq: 1, Type: "MESSAGE", Text: "no bugs", Phase: "REVIEWING", Subagent: "bugs", OccurredAt: at.Add(3 * time.Second)},
			},
		},
		gates: map[string][]storage.GateRun{"#1": {{Name: "go vet", Command: "go vet ./...", ExitCode: 0, Passed: true, FinishedAt: at.Add(90 * time.Second)}}},
	}
}

// TestFeedSelectAgentFiltersThePane proves selecting the review agent shows
// only its events and hides the gate rows, and selecting the implementation
// agent shows its events with the gate rows back.
func TestFeedSelectAgentFiltersThePane(t *testing.T) {
	feed := tui.NewTranscriptFeed(agentFilterFixture())
	pane, err := feed.Poll(context.Background(), "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	all := tui.RenderTranscript(pane)
	for _, want := range []string{"implementing", "reviewing bugs", "go vet"} {
		if !strings.Contains(all, want) {
			t.Fatalf("unfiltered pane omits %q:\n%s", want, all)
		}
	}

	pane = feed.SelectAgent("#1", tui.AgentFilter{Enabled: true, Subagent: "bugs"})
	if pane == nil {
		t.Fatal("SelectAgent returned no pane for a held Issue")
	}
	got := tui.RenderTranscript(pane)
	if strings.Contains(got, "implementing") || strings.Contains(got, "go vet") {
		t.Fatalf("review filter still shows implementation rows:\n%s", got)
	}
	if !strings.Contains(got, "reviewing bugs") || !strings.Contains(got, "no bugs") {
		t.Fatalf("review filter dropped the review rows:\n%s", got)
	}

	pane = feed.SelectAgent("#1", tui.AgentFilter{Enabled: true, Subagent: ""})
	got = tui.RenderTranscript(pane)
	if strings.Contains(got, "reviewing bugs") {
		t.Fatalf("implementation filter still shows review rows:\n%s", got)
	}
	if !strings.Contains(got, "implemented") || !strings.Contains(got, "go vet") {
		t.Fatalf("implementation filter dropped its rows or the gate:\n%s", got)
	}

	// The filter survives the next poll: a tick must not undo the selection.
	pane, err = feed.Poll(context.Background(), "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if got := tui.RenderTranscript(pane); strings.Contains(got, "reviewing bugs") {
		t.Fatalf("a poll undid the agent filter:\n%s", got)
	}
}

// TestFeedSelectAgentUnknownIssueReturnsNil proves a filter on an Issue the
// feed holds no pane for is a no-op.
func TestFeedSelectAgentUnknownIssueReturnsNil(t *testing.T) {
	feed := tui.NewTranscriptFeed(agentFilterFixture())
	if pane := feed.SelectAgent("#9", tui.AgentFilter{Enabled: true}); pane != nil {
		t.Fatal("SelectAgent on an unknown Issue returned a pane")
	}
}

// TestTailerFilterWindowsOverMatchingEvents proves the scrollback window and
// its edges are measured over the matching events alone, so a filtered agent
// with few events still fills the window from its own history.
func TestTailerFilterWindowsOverMatchingEvents(t *testing.T) {
	store := agentFilterFixture()
	tailer := tui.NewTranscriptTailer(store, 1, 0)
	tailer.AddRun(2)
	tailer.SetHeight(1)
	tailer.SetAgentFilter(tui.AgentFilter{Enabled: true, Subagent: "bugs"})
	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(vm.Events) != 1 || vm.Events[0].Text != "no bugs" {
		t.Fatalf("filtered tail window = %+v, want the newest bugs event alone", vm.Events)
	}
	if vm.Retained != 2 || vm.AtStart {
		t.Fatalf("filtered window reports Retained %d AtStart %v, want 2 and false", vm.Retained, vm.AtStart)
	}
	tailer.ScrollUp(1)
	vm, _ = tailer.Poll(context.Background())
	if len(vm.Events) != 1 || vm.Events[0].Text != "reviewing bugs" || !vm.AtStart {
		t.Fatalf("scrolled filtered window = %+v (AtStart %v), want the older bugs event at the start", vm.Events, vm.AtStart)
	}
}

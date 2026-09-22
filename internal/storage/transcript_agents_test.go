package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/storage"
)

// TestTranscriptAgents_OneRowPerRunWithLastEvent proves the query returns one
// summary per AgentRun in run order, each carrying the run's phase, subagent,
// event count, and last event.
func TestTranscriptAgents_OneRowPerRunWithLastEvent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForAgentRun(t, store, "exec-agents", "issue-agents")

	newRun := func() int64 {
		id, err := store.RecordAgentRun(ctx, storage.AgentRun{
			ExecutionID: "exec-agents", IssueID: "issue-agents", Backend: "claude-code",
			StartedAt: time.Now(), FinishedAt: time.Now(), Result: "IMPLEMENTED",
		})
		if err != nil {
			t.Fatalf("RecordAgentRun: %v", err)
		}
		return id
	}
	implRun, bugsRun, docsRun := newRun(), newRun(), newRun()
	at := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

	record := func(runID int64, events []storage.TranscriptEvent) {
		if err := store.RecordTranscriptEvents(ctx, "exec-agents", "issue-agents", runID, events); err != nil {
			t.Fatalf("RecordTranscriptEvents: %v", err)
		}
	}
	record(implRun, []storage.TranscriptEvent{
		{Seq: 0, Type: "MESSAGE", Role: "assistant", Text: "Looking at the issue.", OccurredAt: at, Phase: "IMPLEMENTING"},
		{Seq: 1, Type: "TOOL_CALL", Role: "assistant", ToolName: "Bash", ToolInput: "go build", OccurredAt: at.Add(time.Second), Phase: "IMPLEMENTING"},
	})
	record(bugsRun, []storage.TranscriptEvent{
		{Seq: 0, Type: "MESSAGE", Role: "assistant", Text: "Reviewing bugs.", OccurredAt: at.Add(2 * time.Second), Phase: "REVIEWING", Subagent: "bugs"},
	})
	record(docsRun, []storage.TranscriptEvent{
		{Seq: 0, Type: "MESSAGE", Role: "assistant", Text: "Reviewing docs.", OccurredAt: at.Add(3 * time.Second), Phase: "REVIEWING", Subagent: "docs"},
		{Seq: 1, Type: "MESSAGE", Role: "assistant", Text: "Docs look fine.", OccurredAt: at.Add(4 * time.Second), Phase: "REVIEWING", Subagent: "docs"},
		{Seq: 2, Type: "TOOL_RESULT", Role: "user", ToolName: "Read", ToolOutput: "file body", OccurredAt: at.Add(5 * time.Second), Phase: "REVIEWING", Subagent: "docs"},
	})

	got, err := store.TranscriptAgents(ctx, "exec-agents", "issue-agents")
	if err != nil {
		t.Fatalf("TranscriptAgents: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d agents, want 3: %+v", len(got), got)
	}
	want := []struct {
		run      int64
		phase    string
		subagent string
		events   int
		lastType string
		lastTool string
		lastText string
	}{
		{implRun, "IMPLEMENTING", "", 2, "TOOL_CALL", "Bash", ""},
		{bugsRun, "REVIEWING", "bugs", 1, "MESSAGE", "", "Reviewing bugs."},
		{docsRun, "REVIEWING", "docs", 3, "TOOL_RESULT", "Read", ""},
	}
	for i, w := range want {
		g := got[i]
		if g.AgentRunID != w.run || g.Phase != w.phase || g.Subagent != w.subagent || g.Events != w.events {
			t.Errorf("agent[%d] = %+v, want run %d phase %q subagent %q events %d", i, g, w.run, w.phase, w.subagent, w.events)
		}
		if g.Last.Type != w.lastType || g.Last.ToolName != w.lastTool || g.Last.Text != w.lastText {
			t.Errorf("agent[%d].Last = %+v, want type %q tool %q text %q", i, g.Last, w.lastType, w.lastTool, w.lastText)
		}
	}
	if !got[2].Last.OccurredAt.Equal(at.Add(5 * time.Second)) {
		t.Errorf("agent[2].Last.OccurredAt = %v, want %v", got[2].Last.OccurredAt, at.Add(5*time.Second))
	}
}

// TestTranscriptAgents_NoEventsReturnsEmpty proves an Issue with no recorded
// event yields no agent rather than an error.
func TestTranscriptAgents_NoEventsReturnsEmpty(t *testing.T) {
	store := openTestStore(t)
	got, err := store.TranscriptAgents(context.Background(), "exec-none", "issue-none")
	if err != nil {
		t.Fatalf("TranscriptAgents: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d agents, want 0", len(got))
	}
}

func TestTranscriptAgents_IncludesStartedRunWithoutEvents(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedIssueForAgentRun(t, store, "exec-started", "issue-started")

	runID, err := store.StartAgentRun(ctx, storage.AgentRun{
		ExecutionID: "exec-started",
		IssueID:     "issue-started",
		Backend:     "claude-code",
		StartedAt:   time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC),
		Phase:       "REVIEWING",
		Subagent:    "bugs",
	})
	if err != nil {
		t.Fatalf("StartAgentRun: %v", err)
	}

	agents, err := store.TranscriptAgents(ctx, "exec-started", "issue-started")
	if err != nil {
		t.Fatalf("TranscriptAgents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("got %d agents, want one: %+v", len(agents), agents)
	}
	got := agents[0]
	if got.AgentRunID != runID || got.Phase != "REVIEWING" || got.Subagent != "bugs" || got.Events != 0 {
		t.Fatalf("agent = %+v, want run %d reviewing/bugs with zero events", got, runID)
	}
}

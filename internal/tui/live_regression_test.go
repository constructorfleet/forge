package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestRowDiffKeyIncludesExecutionID(t *testing.T) {
	a := rowDiffKey(WorkerRow{ExecutionID: "ex-a", IssueID: "#1", Verdict: "pass", HasDiff: true})
	b := rowDiffKey(WorkerRow{ExecutionID: "ex-b", IssueID: "#1", Verdict: "pass", HasDiff: true})
	if a == b {
		t.Fatalf("row diff keys collide across executions: %q", a)
	}
}

func TestTranscriptControllerRejectsStaleExecution(t *testing.T) {
	feed := NewTranscriptFeed(nil)
	controller := transcriptController{feed: feed}
	msg := transcriptReadMsg{feed: feed, read: FeedRead{executionID: "ex-a", issueID: "#1"}}
	notice := ""
	var pane *TranscriptPane
	controller.reading = true
	if cmd, committed := controller.applyTranscript(msg, "ex-b", "#1", &notice, &pane, func() tea.Cmd { return nil }); committed || cmd != nil {
		t.Fatalf("stale execution read committed: committed=%v cmd=%v", committed, cmd)
	}
}

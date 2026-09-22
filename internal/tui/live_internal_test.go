package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
)

type diffIdentityStore struct {
	RosterStore
	executionID string
}

func (s *diffIdentityStore) LatestReviewDiff(_ context.Context, executionID, _ string) (string, error) {
	s.executionID = executionID
	return "diff", nil
}

func TestOpenSelectedDiffCapturesExecutionIdentity(t *testing.T) {
	store := &diffIdentityStore{}
	m := NewLiveModel(NewRoster(store, nil), "", 0)
	m.vm.Workers = []WorkerRow{
		{ExecutionID: "ex-a", IssueID: "#1", HasDiff: true},
		{ExecutionID: "ex-b", IssueID: "#1", HasDiff: true},
	}
	m.vm.Selection = 0
	cmd := m.openSelectedDiff()
	if cmd == nil {
		t.Fatal("openSelectedDiff returned nil")
	}
	// A poll or key press can move selection while the diff read is in flight.
	m.vm.Selection = 1
	cmd()
	if store.executionID != "ex-a" {
		t.Fatalf("diff execution id = %q, want ex-a", store.executionID)
	}
}

func TestMaxDiffHorizontalOffsetUsesVisiblePaneWidth(t *testing.T) {
	lines := []DiffLine{{Text: "1234567890"}}
	if got := maxDiffHorizontalOffset(lines, 6); got != 4 {
		t.Fatalf("maxDiffHorizontalOffset = %d, want 4", got)
	}
	if got := maxDiffHorizontalOffset(lines, 12); got != 0 {
		t.Fatalf("maxDiffHorizontalOffset for a wide pane = %d, want 0", got)
	}
}

func TestLiveModelWindowResizeReclampsDiffHorizontalOffset(t *testing.T) {
	m := NewLiveModel(nil, "ex-1", 0)
	m.vm.DiffOpen = true
	m.vm.Diff = &DiffSummary{Lines: []DiffLine{{Text: "a very long diff line"}}}
	m.vm.DiffHorizontal = 100
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	if m.vm.DiffHorizontal > maxDiffHorizontalOffset(m.vm.Diff.Lines, 200) {
		t.Fatalf("DiffHorizontal = %d exceeds resized pane bound", m.vm.DiffHorizontal)
	}
}

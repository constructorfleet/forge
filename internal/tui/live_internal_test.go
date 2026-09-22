package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

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

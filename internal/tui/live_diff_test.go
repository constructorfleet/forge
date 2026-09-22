package tui

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestMaxDiffWidthUsesTerminalCellWidth(t *testing.T) {
	lines := []DiffLine{{Text: "界界a"}}
	if got, want := maxDiffWidth(lines), lipgloss.Width("界界a"); got != want {
		t.Fatalf("maxDiffWidth = %d, want terminal width %d", got, want)
	}
}

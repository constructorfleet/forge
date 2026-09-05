package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// wrap.go: terminal-width row wrapping. A line the pane hands to the terminal
// draws one screen row only while it fits the terminal width; a longer line
// draws several rows, which the row budget must count or the frame overflows
// (see clipTranscript in frame.go). wrapWidth is the one place that turns a
// logical line into the rows it actually draws.
//
// A line may already carry ANSI SGR escape codes from lipgloss styling
// (see Style in style.go). wrapWidth measures and splits on visible cell
// width only, through ansi.Hardwrap, so escape bytes never count toward the
// width budget and a split never falls inside an escape sequence.

// wrapWidth splits line into rows of at most width visible cells, so a line
// the terminal would wrap itself is instead counted as the several rows it
// draws. A width of zero or less applies no wrap: the runtime has not yet
// reported a terminal width, so the caller falls back to unclipped
// rendering.
func wrapWidth(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	if ansi.StringWidth(line) <= width {
		return []string{line}
	}
	return strings.Split(ansi.Hardwrap(line, width, true), "\n")
}

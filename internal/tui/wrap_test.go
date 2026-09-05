package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestClipTranscriptResetsStyleAtWindowTop proves clipTranscript never hands
// the frame a kept window that opens with a dangling SGR style. wrapWidth
// (wrap.go) splits one styled header into several physical rows but only the
// first row carries the opening escape and only the last carries the reset;
// a clip boundary that falls between those rows drops the opening row and
// would otherwise bleed the style into every row after it.
func TestClipTranscriptResetsStyleAtWindowTop(t *testing.T) {
	pane := NewTranscriptPane()
	pane.SetStyle(DefaultStyle())
	pane.SetWidth(5)
	pane.SetView(TranscriptViewModel{
		AtTail:   true,
		RunOrder: []int64{7},
		Events: []TranscriptEvent{
			{AgentRunID: 7, Seq: 0, Type: eventToolCall, ToolName: "abcdefghijklmno", ToolCallID: "t1"},
		},
	})

	// The header wraps into several rows at width 5; clip to fewer rows than
	// the full render so the boundary lands inside the wrapped, styled entry.
	full := len(strings.Split(strings.TrimSuffix(RenderTranscript(pane), "\n"), "\n"))
	got := clipTranscript(pane, full-1)
	if len(got) == 0 {
		t.Fatalf("clipTranscript returned no rows")
	}
	if !strings.HasPrefix(got[0], "\x1b[0m") {
		t.Fatalf("clipTranscript row 0 = %q, want a leading reset so no style bleeds from a dropped row", got[0])
	}
}

// TestWrapWidthPreservesStyleAcrossSplit proves wrapWidth measures a styled
// line by its visible cell width, not its raw byte or rune count, so the SGR
// escape codes lipgloss wraps around a line (e.g. the Faint tool style) never
// count toward the width budget and a split never lands inside an escape
// sequence.
func TestWrapWidthPreservesStyleAcrossSplit(t *testing.T) {
	styled := lipgloss.NewStyle().Faint(true).Render("abcdefghijk")
	got := wrapWidth(styled, 5)
	if len(got) != 3 {
		t.Fatalf("wrapWidth(%q, 5) = %v (len %d), want 3 rows", styled, got, len(got))
	}
	var visible strings.Builder
	for _, row := range got {
		if strings.Count(row, "\x1b[") != strings.Count(row, "m") {
			t.Fatalf("row %q contains an unterminated escape sequence", row)
		}
		visible.WriteString(stripANSI(row))
	}
	if visible.String() != "abcdefghijk" {
		t.Fatalf("visible text = %q, want %q", visible.String(), "abcdefghijk")
	}
}

// stripANSI removes SGR escape sequences, isolating the visible text a row
// draws so tests can compare it independent of styling.
func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestWrapWidthSplitsLongLines proves wrapWidth breaks a line into rows no
// wider than width cells, so a line the terminal would wrap itself is instead
// counted as the several rows it actually draws.
func TestWrapWidthSplitsLongLines(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		width int
		want  []string
	}{
		{"fits inside width", "short", 10, []string{"short"}},
		{"exact width", "abcde", 5, []string{"abcde"}},
		{"one row over", "abcdefghij", 5, []string{"abcde", "fghij"}},
		{"remainder row", "abcdefghijk", 5, []string{"abcde", "fghij", "k"}},
		{"zero width applies no wrap", "abcdefghijk", 0, []string{"abcdefghijk"}},
		{"negative width applies no wrap", "abcdefghijk", -1, []string{"abcdefghijk"}},
		{"empty line", "", 5, []string{""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wrapWidth(tc.line, tc.width)
			if len(got) != len(tc.want) {
				t.Fatalf("wrapWidth(%q, %d) = %v, want %v", tc.line, tc.width, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("wrapWidth(%q, %d) = %v, want %v", tc.line, tc.width, got, tc.want)
				}
			}
		})
	}
}

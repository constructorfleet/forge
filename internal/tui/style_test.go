package tui_test

import (
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/tui"
)

// TestPaneStyleDefaultsToNoColor proves a pane with no style set (the pure,
// headless resting state every existing render test relies on) never embeds
// an ANSI escape, so a no-colour terminal reads it unchanged.
func TestPaneStyleDefaultsToNoColor(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{Events: []tui.TranscriptEvent{
		prose(0, "hello from the agent"),
		call(1, "t1", "bash", "ls"),
	}})

	if got := tui.RenderTranscript(pane); strings.Contains(got, "\x1b[") {
		t.Fatalf("pane with no style embeds ANSI colour: %q", got)
	}
}

// TestPaneStyleDifferentiatesToolCallsFromMessages proves forge's default
// colour scheme (tui.DefaultStyle) colours a tool call distinctly from an
// assistant message, so the two read apart in a colour terminal.
func TestPaneStyleDifferentiatesToolCallsFromMessages(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetStyle(tui.DefaultStyle())
	pane.SetView(tui.TranscriptViewModel{Events: []tui.TranscriptEvent{
		prose(0, "hello from the agent"),
		call(1, "t1", "bash", "ls"),
	}})

	lines := nonEmptyLines(tui.RenderTranscript(pane))
	if len(lines) != 2 {
		t.Fatalf("rendered %d lines, want 2:\n%v", len(lines), lines)
	}
	message, toolCall := lines[0], lines[1]
	if !strings.Contains(message, "\x1b[") {
		t.Errorf("assistant message carries no colour, want the Agent's own voice: %q", message)
	}
	if !strings.Contains(toolCall, "\x1b[") {
		t.Errorf("tool call carries no colour, want it dimmed apart from the message: %q", toolCall)
	}
	if seq(message) == seq(toolCall) {
		t.Errorf("message and tool call share one colour, want each apart: %q vs %q", message, toolCall)
	}
}

// seq returns the leading ANSI SGR sequence of a rendered line, so a test can
// assert two kinds carry different colours without pinning the exact codes.
func seq(line string) string {
	i := strings.Index(line, "m")
	if !strings.HasPrefix(line, "\x1b[") || i < 0 {
		return ""
	}
	return line[:i+1]
}

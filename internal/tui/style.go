package tui

// style.go: the transcript pane's optional colour scheme. A pane's zero-value
// Style holds no rule, so header renders unchanged and every headless render
// test keeps working with no colour. Only a pane the live view builds carries
// a real Style, so an operator's colour terminal reads the Agent's own voice
// apart from a tool call.

import "charm.land/lipgloss/v2"

// Style holds the per-kind text styles a transcript entry's header renders
// through. The zero value applies no style to either kind.
type Style struct {
	// Message styles an assistant MESSAGE entry's header.
	Message lipgloss.Style
	// Tool styles a TOOL_CALL or TOOL_RESULT entry's header.
	Tool lipgloss.Style
}

// DefaultStyle returns forge's terminal colour scheme: an assistant message
// keeps the terminal's own foreground, its plain voice, while a tool call or
// result dims, so it reads as machinery and not the Agent speaking.
func DefaultStyle() Style {
	return Style{Tool: lipgloss.NewStyle().Faint(true)}
}

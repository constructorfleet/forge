package tui

// style.go: the TUI's optional colour scheme. A zero-value Style holds no rule,
// so every renderer draws unchanged and every headless render test keeps
// working with no colour. Only the live view builds a real Style, so an
// operator's colour terminal reads the Agent's own voice apart from a tool
// call, a passed gate apart from a failed one, and a keybinding apart from its
// label.

import "charm.land/lipgloss/v2"

// Style holds the per-kind text styles the transcript pane and the frame render
// through. The zero value of every field applies no colour, so a headless
// render stays plain.
type Style struct {
	// Message styles an assistant MESSAGE entry's header.
	Message lipgloss.Style
	// Tool styles a TOOL_CALL or TOOL_RESULT entry's header.
	Tool lipgloss.Style
	// Truncation styles the reader-side eviction marker, the Agent's own
	// TRUNCATION marker, and the attempt divider: the pane's own notes, not the
	// Agent speaking.
	Truncation lipgloss.Style
	// GatePass styles a passed quality-gate row's header.
	GatePass lipgloss.Style
	// GateFail styles a failed quality-gate row's header.
	GateFail lipgloss.Style
	// Axis styles the inline [axis] review label (bugs, quality, docs).
	Axis lipgloss.Style
	// Key styles the [k] key token in the footer, apart from its label.
	Key lipgloss.Style
	// Selection styles the selected roster or stage row.
	Selection lipgloss.Style
	// Notice styles a roster, transcript, or action notice.
	Notice lipgloss.Style
}

// DefaultStyle returns forge's terminal colour scheme. It uses the terminal's
// own 16-colour palette (indices 0-15), so each colour follows the operator's
// chosen theme and stays readable on a light or a dark background. An assistant
// message reads in the Agent's own voice; a tool call dims to machinery; a
// passed gate reads green and a failed gate red; a review axis, a keybinding,
// and a notice each carry their own hue.
func DefaultStyle() Style {
	return Style{
		Message:    lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
		Tool:       lipgloss.NewStyle().Faint(true),
		Truncation: lipgloss.NewStyle().Faint(true),
		GatePass:   lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		GateFail:   lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true),
		Axis:       lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
		Key:        lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true),
		Selection:  lipgloss.NewStyle().Bold(true),
		Notice:     lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
	}
}

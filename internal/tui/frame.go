package tui

// Package tui renders the live-agent TUI frames. frame.go is a pure function
// from a plain view-model struct to a string: no terminal, no framework
// import, so the whole view is testable headless.

import (
	"fmt"
	"strings"
	"time"

	"github.com/Teagan42/forge/internal/domain"
)

// Attention is the worker-attention glyph state in its own column.
type Attention int

const (
	// AttentionNone: quiet, no operator attention required.
	AttentionNone Attention = iota
	// AttentionNeedsAnswer: worker parked awaiting a human answer.
	AttentionNeedsAnswer
	// AttentionRunningTool: worker has a tool call in flight.
	AttentionRunningTool
)

// AttentionGlyph maps an Attention to its single rune. Blank marks none.
func AttentionGlyph(a Attention) string {
	switch a {
	case AttentionNeedsAnswer:
		return "!"
	case AttentionRunningTool:
		return "*"
	default:
		return " "
	}
}

// Liveness is the worker-liveness glyph state in its own column. It is kept
// separate from Attention so one view can say "needs an answer and its
// orchestrator is gone" without a single precedence-ordered glyph.
type Liveness int

const (
	// LivenessNone: no heartbeat (planning rows claim no liveness).
	LivenessNone Liveness = iota
	// LivenessLive: a heartbeat within the stale window.
	LivenessLive
	// LivenessStale: no heartbeat within the stale window.
	LivenessStale
)

// staleHeartbeat is the age past which a last heartbeat renders as Stale.
const staleHeartbeat = 15 * time.Second

// LivenessGlyph maps a Liveness to its single rune. Blank marks none.
func LivenessGlyph(l Liveness) string {
	switch l {
	case LivenessLive:
		return "\u2022"
	case LivenessStale:
		return "\u00d7"
	default:
		return " "
	}
}

// DeriveAttention derives a row's attention from its state and tool. A parked
// GroupBlocked state flags for a human answer; a running tool flags a busy
// Worker; all else stays quiet.
func DeriveAttention(state domain.IssueState, tool string) Attention {
	if state.Group() == domain.GroupBlocked {
		return AttentionNeedsAnswer
	}
	if tool != "" {
		return AttentionRunningTool
	}
	return AttentionNone
}

// DeriveLiveness derives a row's liveness from heartbeat presence and age.
// Absence (planning) claims no liveness; a beat past staleHeartbeat is stale.
func DeriveLiveness(hasBeat bool, age time.Duration) Liveness {
	if !hasBeat {
		return LivenessNone
	}
	if age > staleHeartbeat {
		return LivenessStale
	}
	return LivenessLive
}

// WorkerRow is one roster line's view data, fully resolved against a clock by
// the caller so the renderer stays time-free and deterministic.
type WorkerRow struct {
	IssueID string
	Title   string
	State   domain.IssueState

	// Elapsed is time spent in the current state (execution_issues.state_changed_at).
	Elapsed time.Duration

	// HasHeartbeat/HeartbeatAge describe the worker's liveness from
	// workers.last_heartbeat. HasHeartbeat false means planning: no claim.
	HasHeartbeat bool
	HeartbeatAge time.Duration

	// Attempt is the 1-based attempt number; Budget the retry ceiling.
	Attempt int
	Budget  int

	// Tool is the running tool's name; empty when none is in flight.
	Tool string

	// Verdict is the aggregate review_runs verdict of the last recorded
	// Review. The per-axis streams ride the transcript pane; the outcome
	// belongs here. Empty until a Review has run.
	Verdict string

	// HasDiff records that the last Review stored a diff, so the frame can
	// offer the pager key. The diff itself never enters the view-model: it is
	// a heavy artifact, read on request and handed to $PAGER.
	HasDiff bool
}

// Pane names the frame's two panes. Focus decides which pane the detail strip
// describes and which keys the footer offers.
type Pane int

const (
	// PaneRoster: the Worker list holds focus.
	PaneRoster Pane = iota
	// PaneTranscript: the transcript pane holds focus.
	PaneTranscript
)

// ViewModel is the plain, transportable input to Render.
type ViewModel struct {
	// Selection is the index of the row whose detail strip and footer render.
	Selection int
	Workers   []WorkerRow

	// Notice explains an empty roster: the Execution does not exist yet, or a
	// poll pass failed. An empty frame with no words reads as a broken TUI.
	Notice string

	// ActionNotice reports the last operator action's outcome when the action
	// declined or failed. It renders whether or not the roster has rows, and it
	// is kept apart from Notice because a poll pass replaces the whole polled
	// view-model: the message must last until the operator presses the next
	// key, not for one poll interval.
	ActionNotice string

	// Transcript is the selected Worker's transcript pane. Nil renders the
	// roster alone.
	Transcript *TranscriptPane

	// Focus names the pane that owns the detail strip and the footer.
	Focus Pane
}

// KeyBinding is one legal key and its label, rendered in the footer.
type KeyBinding struct {
	Key   string
	Label string
}

// String renders a KeyBinding in footer form: [k] label.
func (k KeyBinding) String() string { return "[" + k.Key + "] " + k.Label }

// LegalKeys returns the keys legal for a Worker in state. Derived here so the
// footer always mirrors the rows' own view-model and can never advertise a
// state-illegal key: q is always legal (quit never stops work), c (cancel)
// from any non-terminal state, r (retry) from FAILED, a (answer) while parked
// on NEEDS_INFO.
func LegalKeys(state domain.IssueState) []KeyBinding {
	keys := []KeyBinding{{Key: "q", Label: "quit"}}
	if !state.IsTerminal() {
		keys = append(keys, KeyBinding{Key: "c", Label: "cancel"})
	}
	switch state {
	case domain.StateFailed:
		keys = append(keys, KeyBinding{Key: "r", Label: "retry"})
	case domain.StateNeedsInfo:
		keys = append(keys, KeyBinding{Key: "a", Label: "answer"})
	}
	return keys
}

// Render draws the whole frame: one line per Worker, a notice when the roster
// is empty, an action notice when one is set, the transcript pane, a detail
// strip for the focused pane's selection, and a footer of legal keys. Pure and
// headless.
func Render(vm ViewModel) string {
	var b strings.Builder
	for i, row := range vm.Workers {
		b.WriteString(rowLine(row, i == vm.Selection))
		b.WriteByte('\n')
	}
	if len(vm.Workers) == 0 && vm.Notice != "" {
		b.WriteString(vm.Notice)
		b.WriteByte('\n')
	}
	if vm.ActionNotice != "" {
		b.WriteString(vm.ActionNotice)
		b.WriteByte('\n')
	}
	if vm.Transcript != nil {
		b.WriteString(RenderTranscript(vm.Transcript))
	}
	if strip, ok := stripLine(vm); ok {
		b.WriteString(strip)
		b.WriteByte('\n')
	}
	b.WriteString(footerLine(frameKeys(vm)))
	b.WriteByte('\n')
	return b.String()
}

// stripLine picks the detail strip for the focused pane: the transcript
// selection's own strip, or the selected Worker's.
func stripLine(vm ViewModel) (string, bool) {
	if vm.Focus == PaneTranscript && vm.Transcript != nil {
		if e, ok := vm.Transcript.SelectedEntry(); ok {
			return transcriptDetailLine(e), true
		}
		return "", false
	}
	if row, ok := selectedWorker(vm); ok {
		return detailLine(row), true
	}
	return "", false
}

// frameKeys picks the footer's keys for the focused pane. A transcript focus
// offers only transcript actions, so no state-illegal Worker key can appear.
func frameKeys(vm ViewModel) []KeyBinding {
	if vm.Focus == PaneTranscript && vm.Transcript != nil {
		return TranscriptKeys(vm.Transcript)
	}
	if row, ok := selectedWorker(vm); ok {
		keys := LegalKeys(row.State)
		if row.HasDiff {
			// The diff defers to $PAGER, so the key appears only where the
			// store holds a diff to open.
			keys = append(keys, KeyBinding{Key: "d", Label: "diff"})
		}
		return keys
	}
	return []KeyBinding{{Key: "q", Label: "quit"}}
}

// selectedWorker returns the selected roster row. The bool is false when the
// roster is empty, which a transcript-only attach produces.
func selectedWorker(vm ViewModel) (WorkerRow, bool) {
	if vm.Selection < 0 || vm.Selection >= len(vm.Workers) {
		return WorkerRow{}, false
	}
	return vm.Workers[vm.Selection], true
}

// rowLine renders one Worker. Cursor marks the selection; the coarse state and
// issue id are fixed-width so the title takes whatever width remains.
func rowLine(row WorkerRow, selected bool) string {
	cur := " "
	if selected {
		cur = ">"
	}
	att := AttentionGlyph(DeriveAttention(row.State, row.Tool))
	live := LivenessGlyph(DeriveLiveness(row.HasHeartbeat, row.HeartbeatAge))
	return fmt.Sprintf("%s %s %s %-8s %-10s %s", cur, att, live, row.State.Group(), row.IssueID, row.Title)
}

// detailLine renders the verbatim state, elapsed, heartbeat age, attempt
// against budget, and running tool for the selection.
func detailLine(row WorkerRow) string {
	beat := "\u2014"
	if row.HasHeartbeat {
		beat = formatDuration(row.HeartbeatAge)
	}
	tool := row.Tool
	if tool == "" {
		tool = "\u2014"
	}
	verdict := row.Verdict
	if verdict == "" {
		verdict = "—"
	}
	return fmt.Sprintf("%s | elapsed %s | beat %s | attempt %d/%d | tool %s | verdict %s",
		row.State, formatDuration(row.Elapsed), beat, row.Attempt, row.Budget, tool, verdict)
}

// footerLine joins legal keys into one space-separated footer.
func footerLine(keys []KeyBinding) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k.String()
	}
	return strings.Join(parts, " ")
}

// formatDuration renders a duration compactly as HhMmS or MmS or S.
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Second {
		return "0s"
	}
	secs := int(d / time.Second)
	hrs := secs / 3600
	mins := (secs % 3600) / 60
	secs %= 60
	switch {
	case hrs > 0:
		return fmt.Sprintf("%dh%dm%ds", hrs, mins, secs)
	case mins > 0:
		return fmt.Sprintf("%dm%ds", mins, secs)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

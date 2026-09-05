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

// StaleHeartbeat is the age past which a last heartbeat renders as Stale.
const StaleHeartbeat = 15 * time.Second

// transcriptLagMultiple is how many poll intervals the last committed
// transcript read may age past before the pane header marks it lagging. A
// store slower than one poll interval gates every later tick's read (see
// transcriptController.reading), which thins the transcript's effective
// refresh rate with no signal in the frame; this multiple tells a genuinely
// slow store apart from one poll's own ordinary jitter.
const transcriptLagMultiple = 3

// DeriveTranscriptLag reports whether age, the time since the transcript
// pane's last committed read, exceeds transcriptLagMultiple poll intervals. A
// poll of zero or less means the runtime has not resolved one yet, and reports
// no lag.
func DeriveTranscriptLag(age, poll time.Duration) bool {
	if poll <= 0 {
		return false
	}
	return age > poll*transcriptLagMultiple
}

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
// Absence (planning) claims no liveness. A beat past StaleHeartbeat is stale.
func DeriveLiveness(hasBeat bool, age time.Duration) Liveness {
	if !hasBeat {
		return LivenessNone
	}
	if age > StaleHeartbeat {
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

	// Notice explains the roster: the Execution does not exist yet, it holds no
	// Issues, or a roster poll pass failed. It renders with whatever rows the
	// pass retained, because a stale roster must say so.
	Notice string

	// TranscriptNotice reports a failed transcript poll pass. It is separate
	// from Notice, so neither failure source can hide the other.
	TranscriptNotice string

	// ActionNotice reports the last operator action's outcome when the action
	// declined or failed. It renders whether or not the roster has rows, and it
	// is kept apart from Notice because a poll pass replaces the whole polled
	// view-model: the message must last until the operator presses the next
	// key, not for one poll interval.
	ActionNotice string

	// Transcript is the selected Worker's transcript pane. Nil renders the
	// roster alone.
	Transcript *TranscriptPane

	// TranscriptLagAge is the time since the transcript pane's last committed
	// read. Zero holds before the first commit. DeriveTranscriptLag compares it
	// against PollInterval to mark the pane header, so a store slower than the
	// poll cadence — which the one-in-flight-read guard would otherwise thin
	// silently — is visible to the operator.
	TranscriptLagAge time.Duration

	// PollInterval is the model's own poll cadence, the unit DeriveTranscriptLag
	// measures TranscriptLagAge against. Zero means the runtime has resolved no
	// poll interval yet, and the header marks no lag.
	PollInterval time.Duration

	// Focus names the pane that owns the detail strip and the footer.
	Focus Pane

	// Height is the terminal height in rows. Render clips the transcript to the
	// rows the chrome leaves, so the frame never draws past the terminal bottom.
	// Zero means the runtime has sent no size yet and Render clips nothing.
	Height int

	// Style is the frame's colour scheme: the selected row, the footer keys, and
	// the notices render through it. The zero value applies no colour, which
	// every headless render test relies on; the live view sets forge's real
	// scheme.
	Style Style
}

// KeyBinding is one legal key and its label, rendered in the footer.
type KeyBinding struct {
	Key   string
	Label string
}

// String renders a KeyBinding in footer form: [k] label.
func (k KeyBinding) String() string { return "[" + k.Key + "] " + k.Label }

// IsCancelLegal reports whether cancel is legal for a Worker in state: any
// non-terminal state. LegalKeys and the cancel key handler both call this, so
// the footer's advertised keys and the handler's accepted keys share one
// definition and cannot drift apart.
func IsCancelLegal(state domain.IssueState) bool { return !state.IsTerminal() }

// IsRetryLegal reports whether retry is legal for a Worker in state: only
// FAILED. LegalKeys and the retry key handler both call this, so the
// footer's advertised keys and the handler's accepted keys share one
// definition and cannot drift apart.
func IsRetryLegal(state domain.IssueState) bool { return state == domain.StateFailed }

// IsApproveLegal reports whether approve is legal for a Worker in state: only
// while parked on NEEDS_REPLAN. LegalKeys and the approve key handler both
// call this, so the footer's advertised key and the handler's accepted key
// share one definition and cannot drift apart.
func IsApproveLegal(state domain.IssueState) bool { return state == domain.StateNeedsReplan }

// IsAnswerLegal reports whether answer is legal for a Worker in state: only
// while parked on NEEDS_INFO. LegalKeys and the answer key handler both call
// this, so the footer's advertised key and the handler's accepted key share
// one definition and cannot drift apart.
func IsAnswerLegal(state domain.IssueState) bool { return state == domain.StateNeedsInfo }

// LegalKeys returns the keys legal for a Worker in state. Derived here so the
// footer always mirrors the rows' own view-model and can never advertise a
// state-illegal key: q is always legal (quit never stops work), c (cancel)
// from any non-terminal state, r (retry) from FAILED, a (answer) while parked
// on NEEDS_INFO, p (approve) while parked on NEEDS_REPLAN.
func LegalKeys(state domain.IssueState) []KeyBinding {
	keys := []KeyBinding{{Key: "q", Label: "quit"}}
	if IsCancelLegal(state) {
		keys = append(keys, KeyBinding{Key: "c", Label: "cancel"})
	}
	if IsRetryLegal(state) {
		keys = append(keys, KeyBinding{Key: "r", Label: "retry"})
	}
	if IsAnswerLegal(state) {
		keys = append(keys, KeyBinding{Key: "a", Label: "answer"})
	}
	if IsApproveLegal(state) {
		keys = append(keys, KeyBinding{Key: "p", Label: "approve"})
	}
	return keys
}

// Render draws the whole frame: one line per Worker, a notice for an empty or
// stale roster, the transcript pane with its own poll notice, a detail strip
// for the focused pane's selection, and a footer of legal keys. The transcript
// is clipped to the rows vm.Height leaves, so the frame never draws past the
// terminal bottom. Pure and headless.
func Render(vm ViewModel) string {
	above, below := chromeLines(vm)
	return assembleFrame(above, below, vm.Transcript, vm.Height)
}

// TranscriptRows returns the rows vm.Height leaves the transcript, after the
// roster, the notices, the detail strip, and the footer. Render clips to it and
// the live view budgets the tailer's event window against it, so one place owns
// the arithmetic. Zero means vm.Height is unset and nothing is clipped. One is
// the floor: a terminal too short for the chrome must still show the newest row.
func TranscriptRows(vm ViewModel) int {
	above, below := chromeLines(vm)
	return transcriptRows(vm.Height, above, below)
}

// chromeLines splits the frame's non-transcript rows into the rows above the
// transcript and the rows below it.
func chromeLines(vm ViewModel) (above, below []string) {
	for i, row := range vm.Workers {
		above = append(above, rowLine(row, i == vm.Selection, vm.Style))
	}
	// A notice also carries a failed poll pass, which holds the last good rows.
	// It must render with those rows, or the failure is invisible.
	if vm.Notice != "" {
		above = append(above, vm.Style.Notice.Render(vm.Notice))
	}
	// The notice renders above the pane, so the failure sits with the transcript
	// it describes and a long pane cannot push the two apart.
	if vm.TranscriptNotice != "" {
		above = append(above, vm.Style.Notice.Render(vm.TranscriptNotice))
	}
	// A lagging transcript renders here too, so a store slower than the poll
	// cadence sits with the pane it describes rather than hiding behind a
	// roster that keeps ticking at its own full rate.
	if DeriveTranscriptLag(vm.TranscriptLagAge, vm.PollInterval) {
		above = append(above, vm.Style.Notice.Render(transcriptLagLine(vm.TranscriptLagAge)))
	}
	if vm.ActionNotice != "" {
		above = append(above, vm.Style.Notice.Render(vm.ActionNotice))
	}
	if strip, ok := stripLine(vm); ok {
		below = append(below, strip)
	}
	return above, append(below, footerLine(frameKeys(vm), vm.Style))
}

// assembleFrame joins the chrome rows above and below the transcript with the
// transcript itself, clipped to what height leaves. Shared by Render and
// RenderPlanning so the row order, the clip arithmetic, and the one-row floor
// cannot drift between the execution and planning views.
func assembleFrame(above, below []string, transcript *TranscriptPane, height int) string {
	lines := append(above, clipTranscript(transcript, transcriptRows(height, above, below))...)
	lines = append(lines, below...)
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// transcriptRows returns the rows height leaves the transcript once above and
// below are drawn. Zero means height is unset and nothing is clipped. One is
// the floor: a terminal too short for the chrome must still show the newest
// row.
func transcriptRows(height int, above, below []string) int {
	if height <= 0 {
		return 0
	}
	if rows := height - len(above) - len(below); rows > 1 {
		return rows
	}
	return 1
}

// clipTranscript renders the pane within a row budget, so a pane that draws
// more rows than its event window suggests cannot push the strip and the
// footer off the screen. One event can draw several rows (a divider, the
// eviction marker, a folded result, an expanded block), so the row budget must
// hold here and not at the event window. The pane owns the exact clamp, via
// SetHeight, so it can keep the selected entry's group visible rather than
// blindly keeping whichever rows land at the tail. A zero budget clips
// nothing: the runtime sends no size before the first frame.
func clipTranscript(transcript *TranscriptPane, rows int) []string {
	if transcript == nil {
		return nil
	}
	transcript.SetHeight(rows)
	out := RenderTranscript(transcript)
	if out == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(out, "\n"), "\n")
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
		if len(vm.Workers) > 1 {
			// The switch key moves between concurrently running Workers, so it
			// appears only where there is more than one row to switch to.
			keys = append(keys, KeyBinding{Key: "j/k", Label: "switch worker"})
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
func rowLine(row WorkerRow, selected bool, style Style) string {
	cur := " "
	if selected {
		cur = ">"
	}
	att := AttentionGlyph(DeriveAttention(row.State, row.Tool))
	live := LivenessGlyph(DeriveLiveness(row.HasHeartbeat, row.HeartbeatAge))
	line := fmt.Sprintf("%s %s %s %-8s %-10s %s", cur, att, live, row.State.Group(), row.IssueID, row.Title)
	if selected {
		return style.Selection.Render(line)
	}
	return line
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

// footerLine joins legal keys into one space-separated footer. style colours
// the [k] key token apart from its label. A zero style renders each key as
// KeyBinding.String does, so a headless footer stays plain.
func footerLine(keys []KeyBinding, style Style) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = style.Key.Render("["+k.Key+"]") + " " + k.Label
	}
	return strings.Join(parts, " ")
}

// transcriptLagLine marks the pane header with the age of its last committed
// read, so a store slower than the poll cadence reads as a lagging transcript
// rather than a silently thinned refresh rate.
func transcriptLagLine(age time.Duration) string {
	return fmt.Sprintf("transcript refresh is lagging (last update %s ago)", formatDuration(age))
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

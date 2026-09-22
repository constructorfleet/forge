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
	// ExecutionID identifies the Execution that owns this Issue.
	ExecutionID string
	IssueID     string
	Title       string
	State       domain.IssueState
	// ResumeLegal is true after this session posts an answer for the row.
	ResumeLegal bool

	// Elapsed is time spent in the current state (execution_issues.state_changed_at).
	Elapsed time.Duration

	// HasHeartbeat/HeartbeatAge describe the worker's liveness from
	// workers.last_heartbeat. HasHeartbeat false means planning: no claim.
	HasHeartbeat bool
	HeartbeatAge time.Duration

	// Attempt is the 1-based attempt number; Budget the retry ceiling.
	Attempt int
	Budget  int

	// ProgressDone and ProgressTotal drive the execution table's progress
	// column. Zero values render a neutral dash when no step budget exists.
	ProgressDone  int
	ProgressTotal int

	// Tool is the running tool's name; empty when none is in flight.
	Tool string

	// Verdict is the aggregate review_runs verdict of the last recorded
	// Review. The per-axis streams ride the transcript pane; the outcome
	// belongs here. Empty until a Review has run.
	Verdict string

	// HasDiff records that the last Review stored a diff, so the frame can
	// offer the in-frame hunk view and the full-diff pager key.
	HasDiff bool

	// Agents lists the agents that worked the Issue: the implementation Agent
	// first, then each review subagent. The agents pane renders them and the
	// execution list counts them.
	Agents []AgentRow
}

// Pane names the frame's regions. Focus decides which region the detail strip
// describes and which keys the footer offers. The planning view uses the first
// two alone.
type Pane int

const (
	// PaneRoster: the execution list holds focus.
	PaneRoster Pane = iota
	// PaneTranscript: the output pane holds focus.
	PaneTranscript
	// PaneAgents: the agents pane holds focus.
	PaneAgents
	// PaneDiff: the diff pane holds focus.
	PaneDiff
)

// ViewModel is the plain, transportable input to Render.
type ViewModel struct {
	// ExecutionIDs names every Execution represented by the roster.
	// ExecutionID remains populated for a single-Execution view.
	ExecutionIDs []string
	// ExecutionID names the Execution the list observes. The execution list's
	// header shows its short form.
	ExecutionID string

	// Selection is the index of the row whose detail strip and footer render.
	Selection int
	Workers   []WorkerRow

	// AgentSelection is the index into the selected Worker's Agents of the
	// agent the agents pane marks and the output pane shows.
	AgentSelection int

	// DiffOpen shows the diff pane. Diff is its loaded summary; nil while the
	// read is in flight. DiffScroll is the first hunk line the pane shows.
	// DiffHorizontal is the first column shown for long hunk lines.
	DiffOpen       bool
	Diff           *DiffSummary
	DiffScroll     int
	DiffHorizontal int

	// Width is the terminal width in cells. Render sizes the three body panes
	// from it. Zero means the runtime has sent no size yet, and Render uses
	// defaultFrameWidth.
	Width int

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

// IsResumeLegal reports whether a Worker state can support a resume. The row
// view adds the session answer check before it exposes the control.
func IsResumeLegal(state domain.IssueState) bool { return state == domain.StateNeedsInfo }

// IsResumeLegalForRow reports whether this session posted an answer.
func IsResumeLegalForRow(row WorkerRow) bool {
	return IsResumeLegal(row.State) && row.ResumeLegal
}

// LegalKeys returns the keys legal for a Worker in state. Derived here so the
// footer always mirrors the rows' own view-model and can never advertise a
// state-illegal key: q is always legal (quit never stops work), c (cancel)
// from any non-terminal state, r (retry) from FAILED, a (answer) while parked
// on NEEDS_INFO, R (resume) after an answer, and p (approve) while parked on
// NEEDS_REPLAN.
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

// LegalKeysForRow returns controls legal for one fully resolved row.
func LegalKeysForRow(row WorkerRow) []KeyBinding {
	keys := LegalKeys(row.State)
	if IsResumeLegalForRow(row) {
		keys = append(keys, KeyBinding{Key: "R", Label: "resume"})
	}
	return keys
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

// selectedWorker returns the selected roster row. The bool is false when the
// roster is empty, which a transcript-only attach produces.
func selectedWorker(vm ViewModel) (WorkerRow, bool) {
	if vm.Selection < 0 || vm.Selection >= len(vm.Workers) {
		return WorkerRow{}, false
	}
	return vm.Workers[vm.Selection], true
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

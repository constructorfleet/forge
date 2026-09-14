package tui

// planning_frame.go renders the planning-phase view: the second list model
// docs/specs/live-agent-tui.md section 6 describes, sharing the frame's
// transcript renderer (RenderTranscript), footer shape (KeyBinding,
// footerLine), and pane keys (TranscriptKeys) with the execution roster, but
// with its own row and view-model — planning has no Worker, no IssueState,
// and no liveness to render.

import (
	"fmt"
	"strings"
	"time"
)

// PlanningStageKeys lists the six planning subagent keys wayfinding.Loop can
// invoke, in the order they run. "planning-survey" has no caller and is
// excluded. transcript_events.subagent is the only record of a planning
// run's stage (agent_runs carries no stage column).
var PlanningStageKeys = []string{
	"decision-resolution",
	"planning-readiness-review",
	"specification-generation",
	"specification-review",
	"ticket-plan-generation",
	"ticket-plan-review",
}

// PlanningStageRow is one recorded planning AgentRun's view data: a stage
// label and the timestamp of its last recorded activity. LastActivity never
// claims liveness — planning gets none (docs/specs/live-agent-tui.md
// section 6): finished_at is never NULL, so this is always a completed
// figure, never a running one.
type PlanningStageRow struct {
	Stage        string
	LastActivity time.Time
}

// PlanningViewModel is the plain, transportable input to RenderPlanning. It
// is deliberately not ViewModel: planning has no Worker rows, no
// IssueState-derived legality, and no liveness column, so reusing ViewModel
// would carry fields that mean nothing here.
type PlanningViewModel struct {
	// Stages is the Feature's recorded planning runs, oldest first. The frame
	// renders one collapsed line from it — the current stage (len(Stages)-1),
	// the total attempt count, and the last activity — never one line per run,
	// so a stage that re-runs many times can not grow the strip. Position comes
	// from this run history, never from planning.DeriveStage, which reads the
	// filesystem the read path must not touch.
	Stages []PlanningStageRow

	// Failure is a terminal planning-failure banner. The driver sets it (via
	// PlanningFailedMsg) when the pipeline errors, so the failure and the
	// transcript stay on screen until the operator quits, instead of
	// vanishing with the alternate screen. Empty renders no banner.
	Failure string

	// Notice explains an empty or failed-to-read stage history.
	Notice string
	// TranscriptNotice reports a failed transcript poll pass, kept apart
	// from Notice so neither failure source can hide the other.
	TranscriptNotice string
	// ActionNotice reports the last operator action's outcome.
	ActionNotice string

	// Transcript is the Feature's planning transcript pane: one continuous
	// scrollback across every recorded stage attempt, since the transcript
	// layer unifies across phases (agent_runs/transcript_events share one
	// shape). Nil renders the stage strip alone.
	Transcript *TranscriptPane

	// TranscriptLagAge is the time since the transcript pane's last
	// committed read, as ViewModel.TranscriptLagAge. DeriveTranscriptLag
	// compares it against PollInterval to mark the pane header, so a
	// planning transcript that suffers the same slow-store thinning as the
	// live roster's is visible to the operator too.
	TranscriptLagAge time.Duration

	// PollInterval is the model's own poll cadence, as ViewModel.PollInterval.
	PollInterval time.Duration

	// Focus names the pane that owns the detail strip and the footer.
	Focus Pane
	// Height is the terminal height in rows, as ViewModel.Height.
	Height int

	// Style is the frame's colour scheme, as ViewModel.Style. The zero value
	// applies no colour; the live view sets forge's real scheme.
	Style Style

	// ApproveLegal/AnswerLegal report whether the Feature's Planning
	// Execution is currently parked awaiting the matching control, derived
	// from its store-only PlanningStatus — never from the filesystem.
	ApproveLegal bool
	AnswerLegal  bool

	// DecisionQuestion and DecisionContext hold the pending Decision the
	// answer control replies to. The frame renders them inline while
	// AnswerLegal is true, so the operator reads the question in the panel
	// without opening $EDITOR. Both are empty when no Decision is pending.
	DecisionQuestion string
	DecisionContext  string

	// latestExecutionID is the current Planning Execution's id, carried for
	// the answer control's DecisionCheckpoint lookup only. It is
	// deliberately unexported: the planning-execution UUID must never leak
	// into the rendered UI.
	latestExecutionID string
}

// PlanningLegalKeys returns the keys legal for vm: q is always legal;
// a (answer) only while parked awaiting a Decision answer; p (approve) only
// while parked awaiting an artifact approval. Planning never offers cancel.
func PlanningLegalKeys(vm PlanningViewModel) []KeyBinding {
	keys := []KeyBinding{{Key: "q", Label: "quit"}}
	if vm.AnswerLegal {
		keys = append(keys, KeyBinding{Key: "a", Label: "answer"})
	}
	if vm.ApproveLegal {
		keys = append(keys, KeyBinding{Key: "p", Label: "approve"})
	}
	// tab reaches the transcript pane, where enter expands a row to its full
	// thinking, tool input, or tool output. The hint makes that reach
	// discoverable from the stage view, which otherwise offers only q/a/p.
	if vm.Transcript != nil {
		keys = append(keys, KeyBinding{Key: "tab", Label: "transcript"})
	}
	return keys
}

// RenderPlanning draws the planning frame: one line per recorded stage (the
// newest marked as the live head), a detail strip, the transcript pane
// clipped to the rows vm.Height leaves, and a footer of legal keys. Pure and
// headless, mirroring Render.
func RenderPlanning(vm PlanningViewModel) string {
	above, below := planningChromeLines(vm)
	return assembleFrame(above, below, vm.Transcript, vm.Height)
}

// PlanningTranscriptRows returns the rows vm.Height leaves the transcript,
// the planning analogue of TranscriptRows.
func PlanningTranscriptRows(vm PlanningViewModel) int {
	above, below := planningChromeLines(vm)
	return transcriptRows(vm.Height, above, below)
}

// planningChromeLines splits the planning frame's non-transcript rows into
// the rows above the transcript and the rows below it, mirroring
// chromeLines.
func planningChromeLines(vm PlanningViewModel) (above, below []string) {
	// A terminal failure reads first, above every stage row, in the failed-gate
	// hue, so the operator sees why planning stopped before the run history.
	if vm.Failure != "" {
		above = append(above, vm.Style.GateFail.Render(vm.Failure))
	}
	// The stage strip is one line for the current stage, never one line per
	// recorded attempt. Planning re-runs the same six stages many times, so a
	// per-attempt strip grows without bound and pushes the transcript off
	// screen; the current stage plus an attempt count is the state the
	// operator needs, and the transcript below carries the per-attempt detail.
	if len(vm.Stages) > 0 {
		above = append(above, currentStageLine(vm.Stages, vm.Style))
	}
	// A pending Decision reads inline, so the operator sees the question
	// without pressing the answer key. It sits above the notices, right under
	// the stage line, because it is the one thing that needs an action.
	above = append(above, decisionQuestionLines(vm)...)
	if vm.Notice != "" {
		above = append(above, vm.Style.Notice.Render(vm.Notice))
	}
	if vm.TranscriptNotice != "" {
		above = append(above, vm.Style.Notice.Render(vm.TranscriptNotice))
	}
	// A lagging transcript renders here too, mirroring chromeLines: a store
	// slower than the poll cadence sits with the pane it describes rather
	// than hiding behind a stage strip that keeps ticking at its own rate.
	if DeriveTranscriptLag(vm.TranscriptLagAge, vm.PollInterval) {
		above = append(above, vm.Style.Notice.Render(transcriptLagLine(vm.TranscriptLagAge)))
	}
	if vm.ActionNotice != "" {
		above = append(above, vm.Style.Notice.Render(vm.ActionNotice))
	}
	if strip, ok := planningStripLine(vm); ok {
		below = append(below, strip)
	}
	return above, append(below, footerLine(planningFrameKeys(vm), vm.Style))
}

// planningStripLine picks the detail strip: the transcript selection's own
// strip when the transcript holds focus, otherwise the live head's.
func planningStripLine(vm PlanningViewModel) (string, bool) {
	if vm.Focus == PaneTranscript && vm.Transcript != nil {
		if e, ok := vm.Transcript.SelectedEntry(); ok {
			return transcriptDetailLine(e), true
		}
		return "", false
	}
	if len(vm.Stages) == 0 {
		return "", false
	}
	return stageDetailLine(vm.Stages[len(vm.Stages)-1]), true
}

// planningFrameKeys picks the footer's keys for the focused pane.
func planningFrameKeys(vm PlanningViewModel) []KeyBinding {
	if vm.Focus == PaneTranscript && vm.Transcript != nil {
		return TranscriptKeys(vm.Transcript)
	}
	return PlanningLegalKeys(vm)
}

// currentStageLine renders the one stage line: the current stage (the newest
// recorded row), the total attempt count across the run history, and the last
// activity time. It replaces the old per-attempt strip, so the chrome stays
// one line high however many attempts a stage takes.
func currentStageLine(stages []PlanningStageRow, style Style) string {
	row := stages[len(stages)-1]
	line := fmt.Sprintf("> %-28s (attempt %d)  %s", row.Stage, len(stages), formatLastActivity(row.LastActivity))
	return style.Selection.Render(line)
}

// decisionQuestionLines renders the pending Decision inline: a header naming
// the action, then the question and its context, each wrapped as their own
// commented block. It returns no lines when no Decision is pending, so the
// chrome stays empty until planning parks for an answer.
func decisionQuestionLines(vm PlanningViewModel) []string {
	if !vm.AnswerLegal || vm.DecisionQuestion == "" {
		return nil
	}
	lines := []string{vm.Style.Notice.Render("❓ decision needed (press a to answer)")}
	lines = append(lines, indentedLabelBlock("Q", vm.DecisionQuestion)...)
	if vm.DecisionContext != "" {
		lines = append(lines, indentedLabelBlock("Context", vm.DecisionContext)...)
	}
	return lines
}

// indentedLabelBlock renders text under a label, one physical line per source
// line, so a multi-line question or context keeps its shape. The label opens
// the first line only; later lines indent to align under it.
func indentedLabelBlock(label, text string) []string {
	rows := splitLines(text)
	out := make([]string, 0, len(rows))
	for i, row := range rows {
		if i == 0 {
			out = append(out, fmt.Sprintf("  %s: %s", label, row))
			continue
		}
		out = append(out, fmt.Sprintf("     %s", row))
	}
	return out
}

// splitLines splits text into its lines, dropping a trailing empty line, so a
// block that ends in a newline adds no blank row.
func splitLines(text string) []string {
	rows := strings.Split(strings.TrimRight(text, "\n"), "\n")
	return rows
}

// stageDetailLine renders the live head's stage and last-activity timestamp.
func stageDetailLine(row PlanningStageRow) string {
	return fmt.Sprintf("%s | last activity at %s", row.Stage, formatLastActivity(row.LastActivity))
}

// formatLastActivity renders an absolute timestamp — never an elapsed
// duration or a heartbeat age, since planning claims no liveness.
func formatLastActivity(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

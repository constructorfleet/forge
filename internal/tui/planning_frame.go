package tui

// planning_frame.go renders the planning-phase view: the second list model
// docs/specs/live-agent-tui.md section 6 describes, sharing the frame's
// transcript renderer (RenderTranscript), footer shape (KeyBinding,
// footerLine), and pane keys (TranscriptKeys) with the execution roster, but
// with its own row and view-model — planning has no Worker, no IssueState,
// and no liveness to render.

import (
	"fmt"
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
	// Stages is the Feature's stage-history strip, oldest first. Position —
	// which row is the single live head — comes from this run history
	// (len(Stages)-1), never from planning.DeriveStage, which reads the
	// filesystem the read path must not touch.
	Stages []PlanningStageRow

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
	for i, row := range vm.Stages {
		above = append(above, stageRowLine(row, i == len(vm.Stages)-1, vm.Style))
	}
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

// stageRowLine renders one stage row. head marks the single live head — the
// newest recorded row — with the cursor; no liveness or attention glyph is
// ever drawn, because planning claims neither.
func stageRowLine(row PlanningStageRow, head bool, style Style) string {
	cur := " "
	if head {
		cur = ">"
	}
	line := fmt.Sprintf("%s %-28s %s", cur, row.Stage, formatLastActivity(row.LastActivity))
	if head {
		return style.Selection.Render(line)
	}
	return line
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

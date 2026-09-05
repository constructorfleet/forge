package tui

// planning_model.go is the Bubble Tea model driving the planning-phase
// view: the second list model docs/specs/live-agent-tui.md section 6
// describes. It shares the transcript renderer (RenderTranscript via
// RenderPlanning), the store-polling poller shape, the answer control's
// $EDITOR mechanic, and the actionFlow guard with LiveModel, but polls a
// Feature's stage history instead of a Worker roster and offers no cancel.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Teagan42/forge/internal/needsinfo"
	"github.com/Teagan42/forge/internal/storage"
)

// PlanningApprover is the narrow seam the planning approve key calls into:
// approving the Feature's currently pending planning artifact (whichever
// the latest Planning Execution's NEEDS_APPROVAL status names). Unlike
// Approver.ResumeAfterReplan (which unblocks a coding Issue), this writes no
// Issue state — it approves a Planning Artifact.
type PlanningApprover interface {
	ApprovePlanningArtifact(ctx context.Context, featureID string) error
}

// ErrNoDecisionCheckpoint reports that the store holds no still-pending
// Decision checkpoint for the Feature's latest Planning Execution: a
// NEEDS_HUMAN status was reached without one ever being recorded, or the
// one recorded has already resumed.
var ErrNoDecisionCheckpoint = errors.New("tui: no pending decision checkpoint")

// pendingDecisionCheckpoint returns the one still-unresolved (ResumedAt
// nil) DecisionCheckpoint recorded for planningExecutionID. Planning is
// strictly sequential, so at most one Decision is ever paused at a time.
func pendingDecisionCheckpoint(ctx context.Context, store PlanningRosterStore, planningExecutionID string) (storage.DecisionCheckpoint, error) {
	checkpoints, err := store.GetDecisionCheckpointsByExecution(ctx, planningExecutionID)
	if err != nil {
		return storage.DecisionCheckpoint{}, fmt.Errorf("tui: read decision checkpoints for execution %s: %w", planningExecutionID, err)
	}
	for _, c := range checkpoints {
		if c.ResumedAt == nil {
			return c, nil
		}
	}
	return storage.DecisionCheckpoint{}, ErrNoDecisionCheckpoint
}

// renderDecisionQuestion formats a Decision checkpoint as the $EDITOR
// template the answer key opens, the planning analogue of
// renderNeedsInfoQuestion: the question (and context, if any) Forge asked,
// as commented-out lines, followed by a blank area for the operator's
// answer.
func renderDecisionQuestion(c storage.DecisionCheckpoint) string {
	var b strings.Builder
	b.WriteString("# Write your answer below. Lines starting with # are ignored.\n#\n")
	b.WriteString("# Question:\n")
	writeCommentedLines(&b, stripCommentMarker(c.Question, needsinfo.KindNeedsHuman, c.ExecutionID, c.DecisionID))
	if c.Context != "" {
		b.WriteString("#\n# Context:\n")
		writeCommentedLines(&b, stripCommentMarker(c.Context, needsinfo.KindNeedsHuman, c.ExecutionID, c.DecisionID))
	}
	b.WriteString("\n")
	return b.String()
}

// planningRosterReadMsg carries one finished planning roster read back to
// the update loop, mirroring rosterReadMsg.
type planningRosterReadMsg struct {
	vm  PlanningViewModel
	err error
}

// PlanningModel is the Bubble Tea model driving the planning-phase view for
// one Feature. Like LiveModel, it is an observer, never an owner: it has no
// path to write engineering state directly, so quitting can never stop
// planning. The pure frame RenderPlanning turns the polled
// PlanningViewModel into the view.
type PlanningModel struct {
	Roster    *PlanningRoster
	FeatureID string
	poll      time.Duration
	vm        PlanningViewModel
	lastErr   error

	// transcriptController owns the transcript feed, the read-in-flight
	// guard, ctx, winHeight, and the editor artifact directory, shared with
	// LiveModel. Unlike LiveModel, the feed here is always attached to the
	// Feature's own (executionID, issueID) = (featureID, featureID) pair:
	// planning has no independently selectable rows, so there is nothing to
	// reattach the pane to.
	transcriptController
	rosterReading bool

	// Approver issues ApprovePlanningArtifact. Nil disables the control.
	Approver    PlanningApprover
	approveFlow actionFlow

	// OpenAnswer defers a pending Decision question artifact to $EDITOR.
	// Injected so a test drives the whole key path without spawning a
	// process; nil uses OpenAnswerArtifactInEditor.
	OpenAnswer func(dir, artifact string) tea.Cmd
	// Answerer issues AddComment against the Feature's own tracker issue,
	// mirroring wayfinding.PauseHandler, which posts NEEDS_HUMAN comments to
	// FeatureID rather than the Decision or Planning Execution id. Nil
	// disables the control.
	Answerer   Answerer
	answerFlow actionFlow
}

// NewPlanningModel builds a planning model over r for featureID, polling
// every poll (defaulting to pollInterval when poll is not positive).
func NewPlanningModel(r *PlanningRoster, featureID string, poll time.Duration) *PlanningModel {
	if poll <= 0 {
		poll = pollInterval
	}
	return &PlanningModel{
		Roster:    r,
		FeatureID: featureID,
		poll:      poll,
		transcriptController: transcriptController{
			ctx: context.Background(),
		},
	}
}

// SetFeed attaches the transcript feed each poll drives.
func (m *PlanningModel) SetFeed(f *TranscriptFeed) {
	m.feed = f
	m.applyTranscriptHeight()
	m.reading = false
}

// Init returns a command that paints the first frame immediately, then the
// poll loop takes over.
func (m *PlanningModel) Init() tea.Cmd {
	return func() tea.Msg { return pollTickMsg{time.Now()} }
}

// Update drives the planning poller: a poll tick refetches the stage
// history, starts the transcript read, and schedules the next tick; a
// finished read commits to the frame; q, Ctrl+C, or a programmatic
// interrupt quits the model. There is no cancel key.
func (m *PlanningModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case pollTickMsg:
		next := tea.Tick(m.poll, func(t time.Time) tea.Msg { return pollTickMsg{t} })
		return m, tea.Batch(m.readRoster(), next)
	case planningRosterReadMsg:
		m.applyRoster(msg)
		m.applyTranscriptHeight()
		return m, m.readTranscript()
	case tea.WindowSizeMsg:
		m.winHeight = msg.Height
		m.applyTranscriptHeight()
	case transcriptReadMsg:
		m.applyTranscript(msg)
	case tea.KeyPressMsg:
		key := uv.Key(msg.Key())
		m.vm.ActionNotice = ""
		if key.MatchString("q", "ctrl+c") {
			m.removeArtifacts()
			return m, tea.Quit
		}
		if key.MatchString("p") && m.vm.Focus == PaneRoster {
			return m, m.startApprove()
		}
		if key.MatchString("a") && m.vm.Focus == PaneRoster {
			return m, m.openSelectedAnswer()
		}
		m.handleTranscriptKey(key)
	case answerNoticeMsg:
		m.vm.ActionNotice = msg.text
	case answerReadyMsg:
		m.answerFlow.open(msg.issueID)
		m.vm.ActionNotice = "opening decision question in $EDITOR…"
		return m, m.openAnswer(msg.dir, msg.artifact)
	case AnswerClosedMsg:
		if msg.Err != nil {
			m.answerFlow.close()
			m.vm.ActionNotice = msg.Err.Error()
			return m, nil
		}
		answer := extractAnswer(msg.Text)
		if answer == "" {
			m.answerFlow.close()
			m.vm.ActionNotice = "answer is empty, not posted"
			return m, nil
		}
		return m, m.startAnswer(answer)
	case answerResultMsg:
		m.applyAnswerResult(msg)
	case approveResultMsg:
		m.applyApproveResult(msg)
	case tea.InterruptMsg:
		m.removeArtifacts()
		return m, tea.Quit
	}
	return m, nil
}

// readRoster returns the command that reads the planning stage history.
func (m *PlanningModel) readRoster() tea.Cmd {
	if m.rosterReading {
		return nil
	}
	m.rosterReading = true
	roster, ctx, featureID := m.Roster, m.ctx, m.FeatureID
	return func() tea.Msg {
		vm, err := roster.Fetch(ctx, featureID)
		return planningRosterReadMsg{vm: vm, err: err}
	}
}

// applyRoster commits a finished roster read and preserves pane-owned
// state, mirroring LiveModel.applyRoster.
func (m *PlanningModel) applyRoster(msg planningRosterReadMsg) {
	m.rosterReading = false
	m.lastErr = msg.err
	if msg.err != nil {
		m.vm.Notice = msg.err.Error()
		return
	}
	vm := msg.vm
	vm.Transcript, vm.Focus = m.vm.Transcript, m.vm.Focus
	vm.ActionNotice = m.vm.ActionNotice
	vm.TranscriptNotice = m.vm.TranscriptNotice
	m.vm = vm
}

// readTranscript returns the command that reads the Feature's planning
// transcript: one continuous scrollback across every recorded stage
// attempt, since the transcript layer unifies across phases.
func (m *PlanningModel) readTranscript() tea.Cmd {
	if m.feed == nil || m.reading {
		return nil
	}
	m.reading = true
	feed, ctx, featureID := m.feed, m.ctx, m.FeatureID
	return func() tea.Msg {
		return transcriptReadMsg{feed: feed, read: feed.Fetch(ctx, featureID, featureID)}
	}
}

// applyTranscript commits a finished read, mirroring LiveModel.applyTranscript.
func (m *PlanningModel) applyTranscript(msg transcriptReadMsg) {
	m.transcriptController.applyTranscript(msg, &m.vm.TranscriptNotice, &m.vm.Transcript)
}

// applyTranscriptHeight sizes the tailer's event window from the transcript
// row budget, mirroring LiveModel.applyTranscriptHeight.
func (m *PlanningModel) applyTranscriptHeight() {
	m.vm.Height = m.winHeight
	m.sizeFeed(PlanningTranscriptRows(m.vm))
}

// handleTranscriptKey applies the pane keys, mirroring
// LiveModel.handleTranscriptKey.
func (m *PlanningModel) handleTranscriptKey(key uv.Key) {
	m.transcriptController.handleTranscriptKey(key, m.vm.Transcript, &m.vm.Focus)
}

// startApprove returns the command that runs ApprovePlanningArtifact off the
// update goroutine. Unlike the execution approve control, planning approval
// defers to no $PAGER preview: the store holds no artifact preview for it,
// only the NEEDS_APPROVAL status that gates legality.
func (m *PlanningModel) startApprove() tea.Cmd {
	if m.approveFlow.guard(&m.vm.ActionNotice, "approve") {
		return nil
	}
	if !m.vm.ApproveLegal {
		m.vm.ActionNotice = "no approvable planning artifact"
		return nil
	}
	if m.Approver == nil {
		m.vm.ActionNotice = "approve is not available"
		return nil
	}
	m.approveFlow.open(m.FeatureID)
	m.vm.ActionNotice = fmt.Sprintf("approving planning artifact for %s…", m.FeatureID)
	approver, ctx, featureID := m.Approver, m.ctx, m.FeatureID
	return func() tea.Msg {
		err := approver.ApprovePlanningArtifact(ctx, featureID)
		return approveResultMsg{issueID: featureID, err: err}
	}
}

// applyApproveResult commits a finished approve call, mirroring
// LiveModel.applyApproveResult.
func (m *PlanningModel) applyApproveResult(msg approveResultMsg) {
	m.approveFlow.applyResult(&m.vm.ActionNotice, msg.issueID, msg.err, "approve", "approve requested for %s")
}

// openSelectedAnswer reads the Feature's pending Decision checkpoint and
// defers it to $EDITOR. It returns no command when no Decision is pending,
// and reports that on the notice rather than opening an editor for nothing.
func (m *PlanningModel) openSelectedAnswer() tea.Cmd {
	if m.answerFlow.guard(&m.vm.ActionNotice, "answer") {
		return nil
	}
	if !m.vm.AnswerLegal {
		m.vm.ActionNotice = "no answerable decision"
		return nil
	}
	dir, err := m.artifactDir()
	if err != nil {
		m.vm.ActionNotice = err.Error()
		return nil
	}
	executionID, featureID, store := m.vm.latestExecutionID, m.FeatureID, m.Roster.Store
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), diffReadTimeout)
		defer cancel()
		checkpoint, err := pendingDecisionCheckpoint(ctx, store, executionID)
		if err != nil {
			return answerNoticeMsg{text: err.Error()}
		}
		return answerReadyMsg{dir: dir, issueID: featureID, artifact: renderDecisionQuestion(checkpoint)}
	}
}

// openAnswer defers a read Decision question to $EDITOR through the
// injected opener.
func (m *PlanningModel) openAnswer(dir, artifact string) tea.Cmd {
	open := m.OpenAnswer
	if open == nil {
		open = OpenAnswerArtifactInEditor
	}
	return open(dir, artifact)
}

// startAnswer returns the command that runs AddComment off the update
// goroutine, posting the answer to the Feature's own tracker issue.
func (m *PlanningModel) startAnswer(answer string) tea.Cmd {
	if m.Answerer == nil {
		m.answerFlow.close()
		m.vm.ActionNotice = "answer is not available"
		return nil
	}
	featureID := m.answerFlow.issueID
	m.vm.ActionNotice = fmt.Sprintf("posting answer for %s…", featureID)
	answerer, ctx := m.Answerer, m.ctx
	return func() tea.Msg {
		_, err := answerer.AddComment(ctx, featureID, answer)
		return answerResultMsg{issueID: featureID, err: err}
	}
}

// applyAnswerResult commits a finished answer post, mirroring
// LiveModel.applyAnswerResult.
func (m *PlanningModel) applyAnswerResult(msg answerResultMsg) {
	m.answerFlow.applyResult(&m.vm.ActionNotice, msg.issueID, msg.err, "answer", "answer posted for %s")
}

// artifactDir returns this session's $EDITOR artifact directory, creating it
// on first use.
func (m *PlanningModel) artifactDir() (string, error) {
	return m.transcriptController.artifactDir("forge-planning-*")
}

// Close drops any artifact the session wrote. The caller defers it around
// the Bubble Tea program.
func (m *PlanningModel) Close() { m.removeArtifacts() }

// View renders the current frame headless.
func (m *PlanningModel) View() tea.View { return tea.NewView(RenderPlanning(m.vm)) }

// Stages exposes the current stage-history rows, for the model's own tests.
func (m *PlanningModel) Stages() []PlanningStageRow { return m.vm.Stages }

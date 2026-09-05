package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// pollInterval is the ~1s cadence between roster polls, per the observation
// seam's spec.
const pollInterval = 1 * time.Second

// diffReadTimeout bounds the on-demand diff read. The diff column can hold a
// large blob, and the read runs on the event loop.
const diffReadTimeout = 2 * time.Second

// pollTickMsg carries the clock time at which a poll pass ran. The time is
// baked into the message so the whole pass is deterministic whatever the wall
// clock does.
type pollTickMsg struct{ now time.Time }

// rosterReadMsg carries one finished roster read back to the update loop. The
// read runs in a command, so the store never blocks the key handler.
type rosterReadMsg struct {
	vm  ViewModel
	err error
}

// transcriptReadMsg carries one finished feed read back to the update loop. The
// read runs in a command, so the store never blocks the key handler; the model
// commits it here, where it is the only writer of the pane. feed names the reader,
// so a read still in flight when the feed changes is dropped rather than
// committed to a feed that never asked for it.
type transcriptReadMsg struct {
	feed *TranscriptFeed
	read FeedRead
}

// LiveModel is the Bubble Tea model driving the live roster for one
// Execution: it polls the Roster each tick and renders the frame. It is an
// observer, never an owner (ADR-0031): it has no path to write engineering
// state, so quitting (q / Ctrl+C) can never stop the work being watched. The
// pure frame Render turns the polled ViewModel into the view.
type LiveModel struct {
	Roster      *Roster
	ExecutionID string
	poll        time.Duration
	vm          ViewModel
	lastErr     error

	// transcriptController owns the transcript feed, the read-in-flight
	// guard, ctx, winHeight, and the pager artifact directory, shared with
	// PlanningModel.
	transcriptController

	// rosterReading records a roster read in flight, so a tick starts no second
	// one while a slow store still reads.
	rosterReading bool

	// OpenDiff defers a diff to $PAGER, writing its artifact under the given
	// directory. Injected so a test drives the whole key path without spawning
	// a process; nil uses OpenDiffInPager.
	OpenDiff func(dir, diff string) tea.Cmd

	// Canceller issues CancelExecution. Nil disables the control: the cancel
	// key then explains itself instead of silently doing nothing.
	Canceller Canceller

	// confirming records that a cancel key armed the UI-only confirmation and
	// awaits the operator's next key to fire or abandon it.
	confirming bool
	// cancelling records a CancelExecution call in flight, so a second cancel
	// key press on the same call cannot double-issue it.
	cancelling bool

	// Retrier spawns a detached forge retry child. Nil disables the control:
	// the retry key then explains itself instead of silently doing nothing.
	Retrier Retrier

	// retrying records a Retry call in flight, so a second retry key press on
	// the same call cannot double-issue it.
	retrying bool

	// OpenApprove defers a replan-checkpoint artifact to $PAGER, writing it
	// under the given directory. Injected so a test drives the whole key path
	// without spawning a process; nil uses OpenApprovalArtifactInPager.
	OpenApprove func(dir, artifact string) tea.Cmd

	// Approver issues ResumeAfterReplan. Nil disables the control: the
	// approve key then explains itself instead of silently doing nothing.
	Approver Approver

	// approveFlow tracks an approve flow in flight — from the pager opening
	// through ResumeAfterReplan returning — so a second approve key press on
	// the same row cannot double-issue it.
	approveFlow actionFlow

	// OpenAnswer defers a needs-info question artifact to $EDITOR, writing it
	// under the given directory. Injected so a test drives the whole key path
	// without spawning a process; nil uses OpenAnswerArtifactInEditor.
	OpenAnswer func(dir, artifact string) tea.Cmd

	// Answerer issues AddComment. Nil disables the control: the answer key
	// then explains itself instead of silently doing nothing.
	Answerer Answerer

	// answerFlow tracks an answer flow in flight — from the editor opening
	// through AddComment returning — so a second answer key press on the
	// same row cannot double-post it.
	answerFlow actionFlow
}

// NewLiveModel builds a live roster model over r for executionID, polling
// every poll (defaulting to pollInterval when poll is not positive).
func NewLiveModel(r *Roster, executionID string, poll time.Duration) *LiveModel {
	if poll <= 0 {
		poll = pollInterval
	}
	return &LiveModel{
		Roster:      r,
		ExecutionID: executionID,
		poll:        poll,
		transcriptController: transcriptController{
			ctx: context.Background(),
		},
	}
}

// Init returns a command that paints the first frame immediately, then the
// poll loop takes over from the injected clock.
func (m *LiveModel) Init() tea.Cmd {
	return func() tea.Msg { return pollTickMsg{m.Roster.Now()} }
}

// Update drives the roster: a poll tick refetches state, starts the transcript
// read, and schedules the next tick; a finished read commits to the pane; q,
// Ctrl+C, or a programmatic interrupt quits the model. Quitting carries no
// stop-work signal.
func (m *LiveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case pollTickMsg:
		next := tea.Tick(m.poll, func(t time.Time) tea.Msg { return pollTickMsg{t} })
		return m, tea.Batch(m.readRoster(msg.now), next)
	case rosterReadMsg:
		m.applyRoster(msg)
		// The roster row count is part of the chrome, so a pass that changed the
		// rows changes the transcript height as well.
		m.applyTranscriptHeight()
		return m, m.readTranscript()
	case tea.WindowSizeMsg:
		m.winHeight = msg.Height
		m.applyTranscriptHeight()
	case transcriptReadMsg:
		m.applyTranscript(msg)
	case tea.KeyPressMsg:
		key := uv.Key(msg.Key())
		// Any key press answers the last action notice, so it clears here.
		m.vm.ActionNotice = ""
		if key.MatchString("q", "ctrl+c") {
			m.removeArtifacts()
			return m, tea.Quit
		}
		if m.confirming {
			return m, m.resolveCancelConfirm(key)
		}
		if key.MatchString("c") && m.vm.Focus == PaneRoster {
			return m, m.armCancelConfirm()
		}
		if key.MatchString("d") && m.vm.Focus == PaneRoster {
			return m, m.openSelectedDiff()
		}
		if key.MatchString("r") && m.vm.Focus == PaneRoster {
			return m, m.startRetry()
		}
		if key.MatchString("p") && m.vm.Focus == PaneRoster {
			return m, m.openSelectedApprove()
		}
		if key.MatchString("a") && m.vm.Focus == PaneRoster {
			return m, m.openSelectedAnswer()
		}
		m.handleTranscriptKey(key)
	case diffNoticeMsg:
		m.vm.ActionNotice = msg.text
	case diffReadyMsg:
		return m, m.openDiff(msg.dir, msg.diff)
	case diffClosedMsg:
		if msg.err != nil {
			m.vm.ActionNotice = msg.err.Error()
		}
	case cancelResultMsg:
		m.applyCancelResult(msg)
	case retryResultMsg:
		m.applyRetryResult(msg)
	case approveNoticeMsg:
		m.vm.ActionNotice = msg.text
	case approveReadyMsg:
		m.approveFlow.open(msg.issueID)
		m.vm.ActionNotice = fmt.Sprintf("opening replan artifact for %s in $PAGER…", msg.issueID)
		return m, m.openApprove(msg.dir, msg.artifact)
	case ApproveClosedMsg:
		if msg.Err != nil {
			m.approveFlow.close()
			m.vm.ActionNotice = msg.Err.Error()
			return m, nil
		}
		return m, m.startApprove()
	case approveResultMsg:
		m.applyApproveResult(msg)
	case answerNoticeMsg:
		m.vm.ActionNotice = msg.text
	case answerReadyMsg:
		m.answerFlow.open(msg.issueID)
		m.vm.ActionNotice = fmt.Sprintf("opening needs-info question for %s in $EDITOR…", msg.issueID)
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
	case tea.InterruptMsg:
		// The TUI's own suspend signal binds to q too.
		m.removeArtifacts()
		return m, tea.Quit
	}
	return m, nil
}

// readRoster returns the command that reads the roster state. The read runs in
// the command, off the update goroutine, so a slow store cannot delay a key
// press.
func (m *LiveModel) readRoster(now time.Time) tea.Cmd {
	if m.rosterReading {
		return nil
	}
	m.rosterReading = true
	roster, ctx, executionID := m.Roster, m.ctx, m.ExecutionID
	return func() tea.Msg {
		vm, err := roster.Fetch(ctx, executionID, now)
		return rosterReadMsg{vm: vm, err: err}
	}
}

// applyRoster commits a finished roster read and preserves pane-owned state. A
// read failure holds the last good rows: one transient read must not blank the
// frame for a tick.
func (m *LiveModel) applyRoster(msg rosterReadMsg) {
	m.rosterReading = false
	m.lastErr = msg.err
	if msg.err != nil {
		// A silent poll failure is indistinguishable from an idle roster.
		m.vm.Notice = msg.err.Error()
		return
	}
	vm := msg.vm
	// The roster refresh keeps the operator's pane and focus. The feed read owns
	// the pane, and it detaches the pane when the selected row disappears.
	vm.Transcript, vm.Focus = m.vm.Transcript, m.vm.Focus
	vm.ActionNotice = m.vm.ActionNotice
	vm.TranscriptNotice = m.vm.TranscriptNotice
	m.vm = vm
}

// openSelectedDiff reads the selected Worker's stored Review diff and defers it
// to $PAGER. It returns no command when the store holds no diff, and reports
// that on the notice rather than opening an empty pager.
func (m *LiveModel) openSelectedDiff() tea.Cmd {
	row, ok := selectedWorker(m.vm)
	if !ok {
		m.vm.ActionNotice = "no Worker selected"
		return nil
	}
	// The directory is model state, so the event loop creates it. Only the store
	// read runs inside the command, where a large blob cannot block the frame.
	dir, err := m.artifactDir()
	if err != nil {
		m.vm.ActionNotice = err.Error()
		return nil
	}
	issueID := row.IssueID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), diffReadTimeout)
		defer cancel()
		diff, err := LatestDiff(ctx, m.Roster.Store, m.ExecutionID, issueID)
		if err != nil {
			if errors.Is(err, ErrNoDiff) {
				return diffNoticeMsg{text: fmt.Sprintf("no diff for %s yet", issueID)}
			}
			return diffNoticeMsg{text: err.Error()}
		}
		return diffReadyMsg{dir: dir, diff: diff}
	}
}

// diffNoticeMsg carries an explanation for a diff key that opened no pager.
type diffNoticeMsg struct{ text string }

// diffReadyMsg carries a read diff back to the event loop, which owns the pager
// handover: tea.ExecProcess must come from Update and not from inside a command.
type diffReadyMsg struct {
	dir  string
	diff string
}

// openDiff defers a read diff to $PAGER through the injected opener.
func (m *LiveModel) openDiff(dir, diff string) tea.Cmd {
	open := m.OpenDiff
	if open == nil {
		open = OpenDiffInPager
	}
	return open(dir, diff)
}

// artifactDir returns this session's pager-artifact directory (diffs and
// replan checkpoints), creating it on first use.
func (m *LiveModel) artifactDir() (string, error) {
	return m.transcriptController.artifactDir("forge-diffs-*")
}

// Close drops every pager artifact the session wrote. The caller defers it
// around the Bubble Tea program, so an exit path other than the quit keys — a
// context cancellation, a signal, or a panic — leaks no temp directory.
func (m *LiveModel) Close() { m.removeArtifacts() }

// handleTranscriptKey applies the pane keys. Tab moves focus; the movement
// and expand keys act only while the pane holds focus, so a roster key and a
// pane key can share a rune without collision.
func (m *LiveModel) handleTranscriptKey(key uv.Key) {
	m.transcriptController.handleTranscriptKey(key, m.vm.Transcript, &m.vm.Focus)
}

// readTranscript returns the command that reads the selected Worker's
// transcript. The read runs in the command, off the update goroutine, so a slow
// store cannot delay a key press. No selected Worker detaches the pane at once
// and starts no read: a transcript that belongs to no listed row must not keep
// rendering.
//
// One read runs at a time. A store slower than the poll interval would otherwise
// leave several reads in flight over the one tailer, whose cursors only Fetch
// reads and only Apply advances: the later read would re-read the events the
// earlier one holds and append them twice.
func (m *LiveModel) readTranscript() tea.Cmd {
	if m.feed == nil || m.reading {
		return nil
	}
	row, ok := selectedWorker(m.vm)
	if !ok {
		m.detachTranscript()
		return nil
	}
	m.reading = true
	feed, ctx, executionID, issueID := m.feed, m.ctx, m.ExecutionID, row.IssueID
	return func() tea.Msg {
		return transcriptReadMsg{feed: feed, read: feed.Fetch(ctx, executionID, issueID)}
	}
}

// applyTranscript commits a finished read and attaches the pane it produces. A
// read failure keeps the pane the feed already holds and reports the failure in
// TranscriptNotice, so a transient failure never blanks the transcript.
func (m *LiveModel) applyTranscript(msg transcriptReadMsg) {
	m.transcriptController.applyTranscript(msg, &m.vm.TranscriptNotice, &m.vm.Transcript)
}

// applyTranscriptHeight sizes the tailer's event window from the transcript row
// budget. The two units differ: the tailer counts events and the budget counts
// rows, and one event can draw several rows. So this is an upper bound on how
// much history to read, and Render owns the exact clip to the terminal. It runs
// on a resize, on each poll (the roster row count is part of the chrome), and
// when a feed is attached.
func (m *LiveModel) applyTranscriptHeight() {
	m.vm.Height = m.winHeight
	m.sizeFeed(TranscriptRows(m.vm))
}

// SetFeed attaches the transcript feed each poll drives. It is the pane's one
// owner. A nil feed renders the roster alone.
func (m *LiveModel) SetFeed(f *TranscriptFeed) {
	m.feed = f
	m.applyTranscriptHeight()
	// The read in flight belongs to the old feed and its message is dropped, so
	// the new feed must be free to start its own.
	m.reading = false
	m.detachTranscript()
}

// detachTranscript drops the pane and returns focus to the roster, leaving the
// feed attached.
func (m *LiveModel) detachTranscript() {
	m.vm.Transcript = nil
	m.vm.TranscriptNotice = ""
	m.vm.Focus = PaneRoster
}

// View renders the current frame headless. It claims the alternate screen
// buffer, so the terminal redraws the whole frame from a fixed top on every
// poll instead of appending it below the last one: without it, a frame
// taller than the terminal scrolls earlier frames above the visible window
// and the header stops tracking the top row.
func (m *LiveModel) View() tea.View {
	v := tea.NewView(Render(m.vm))
	v.AltScreen = true
	return v
}

// Workers exposes the current roster rows, for the model's own tests.
func (m *LiveModel) Workers() []WorkerRow { return m.vm.Workers }

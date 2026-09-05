package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
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

	// feed drives the transcript pane for the selected Worker. It is the pane's
	// one owner: a nil feed renders the roster alone.
	feed *TranscriptFeed
	// rosterReading records a roster read in flight, so a tick starts no second
	// one while a slow store still reads.
	rosterReading bool
	// reading records a feed read in flight, so a tick starts no second one.
	reading bool
	// ctx bounds the feed reads the poll commands run, so quitting the program
	// cancels an in-flight store read instead of waiting it out.
	ctx context.Context

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

	// winHeight is the last terminal height the runtime reported. Zero means the
	// runtime has sent no size yet: the frame then clips nothing and the tailer
	// keeps its own default.
	winHeight int

	// diffDir holds this session's diff artifacts. A pager killed before its
	// own cleanup callback runs leaves a file, so quit removes the directory.
	diffDir string
}

// NewLiveModel builds a live roster model over r for executionID, polling
// every poll (defaulting to pollInterval when poll is not positive).
func NewLiveModel(r *Roster, executionID string, poll time.Duration) *LiveModel {
	if poll <= 0 {
		poll = pollInterval
	}
	return &LiveModel{Roster: r, ExecutionID: executionID, poll: poll, ctx: context.Background()}
}

// SetContext bounds every store read the model performs. Pass the program's own
// context, so a quit cancels an in-flight read.
func (m *LiveModel) SetContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.ctx = ctx
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
			m.removeDiffArtifacts()
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
	case tea.InterruptMsg:
		// The TUI's own suspend signal binds to q too.
		m.removeDiffArtifacts()
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
	dir, err := m.diffArtifactDir()
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

// diffArtifactDir returns this session's artifact directory, creating it on
// first use.
func (m *LiveModel) diffArtifactDir() (string, error) {
	if m.diffDir != "" {
		return m.diffDir, nil
	}
	dir, err := os.MkdirTemp("", "forge-diffs-*")
	if err != nil {
		return "", fmt.Errorf("tui: create diff artifact directory: %w", err)
	}
	m.diffDir = dir
	return dir, nil
}

// Close drops every diff artifact the session wrote. The caller defers it
// around the Bubble Tea program, so an exit path other than the quit keys — a
// context cancellation, a signal, or a panic — leaks no temp directory.
func (m *LiveModel) Close() { m.removeDiffArtifacts() }

// removeDiffArtifacts drops every artifact the session wrote. A failure is
// silent: an observer never aborts on temp-file cleanup.
func (m *LiveModel) removeDiffArtifacts() {
	if m.diffDir == "" {
		return
	}
	_ = os.RemoveAll(m.diffDir)
	m.diffDir = ""
}

// handleTranscriptKey applies the pane keys. Tab moves focus; the movement
// and expand keys act only while the pane holds focus, so a roster key and a
// pane key can share a rune without collision.
func (m *LiveModel) handleTranscriptKey(key uv.Key) {
	pane := m.vm.Transcript
	if pane == nil {
		return
	}
	if key.MatchString("tab") {
		if m.vm.Focus == PaneTranscript {
			m.vm.Focus = PaneRoster
			return
		}
		m.vm.Focus = PaneTranscript
		return
	}
	if m.vm.Focus != PaneTranscript {
		return
	}
	switch {
	case key.MatchString("k", "up"):
		pane.MoveSelection(-1)
	case key.MatchString("j", "down"):
		pane.MoveSelection(1)
	case key.MatchString("enter"):
		pane.ToggleExpand()
	case key.MatchString("G"):
		pane.FollowTail()
	}
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
	if m.feed == nil || msg.feed != m.feed {
		return
	}
	m.reading = false
	pane := m.feed.Apply(msg.read)
	m.vm.TranscriptNotice = msg.read.Err()
	if pane != nil {
		m.vm.Transcript = pane
	}
}

// applyTranscriptHeight sizes the tailer's event window from the transcript row
// budget. The two units differ: the tailer counts events and the budget counts
// rows, and one event can draw several rows. So this is an upper bound on how
// much history to read, and Render owns the exact clip to the terminal. It runs
// on a resize, on each poll (the roster row count is part of the chrome), and
// when a feed is attached.
func (m *LiveModel) applyTranscriptHeight() {
	m.vm.Height = m.winHeight
	rows := TranscriptRows(m.vm)
	if m.feed == nil || rows <= 0 {
		return
	}
	m.feed.SetHeight(rows)
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

// View renders the current frame headless.
func (m *LiveModel) View() tea.View {
	return tea.NewView(Render(m.vm))
}

// Workers exposes the current roster rows, for the model's own tests.
func (m *LiveModel) Workers() []WorkerRow { return m.vm.Workers }

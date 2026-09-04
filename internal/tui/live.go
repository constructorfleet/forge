package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// pollInterval is the ~1s cadence between roster polls, per the observation
// seam's spec.
const pollInterval = 1 * time.Second

// pollTickMsg carries the clock time at which a poll pass ran. The time is
// baked into the message so the whole pass is deterministic whatever the wall
// clock does.
type pollTickMsg struct{ now time.Time }

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
	// reading records a feed read in flight, so a tick starts no second one.
	reading bool
	// ctx bounds the feed reads the poll commands run, so quitting the program
	// cancels an in-flight store read instead of waiting it out.
	ctx context.Context
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
		vm, err := m.Roster.Fetch(m.ctx, m.ExecutionID, msg.now)
		m.lastErr = err
		if err != nil {
			// A silent poll failure is indistinguishable from an idle roster.
			// Fetch returns a zero ViewModel on error, so hold the last good
			// rows: one transient read must not blank the frame for a tick.
			m.vm.Notice = err.Error()
		} else {
			// The roster refresh keeps the operator's pane and focus: they are
			// not the roster's to reset. The feed read the tick starts owns the
			// pane, and it detaches the pane and returns focus to the roster
			// when the selected row disappears.
			vm.Transcript, vm.Focus = m.vm.Transcript, m.vm.Focus
			m.vm = vm
		}
		// The feed read comes first in the batch, so a caller that drives the
		// model by hand reaches it without waiting out the tick.
		next := tea.Tick(m.poll, func(t time.Time) tea.Msg { return pollTickMsg{t} })
		return m, tea.Batch(m.readTranscript(), next)
	case transcriptReadMsg:
		m.applyTranscript(msg)
	case tea.KeyPressMsg:
		key := uv.Key(msg.Key())
		if key.MatchString("q", "ctrl+c") {
			return m, tea.Quit
		}
		m.handleTranscriptKey(key)
	case tea.InterruptMsg:
		// The TUI's own suspend signal binds to q too.
		return m, tea.Quit
	}
	return m, nil
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

// SetFeed attaches the transcript feed each poll drives. It is the pane's one
// owner. A nil feed renders the roster alone.
func (m *LiveModel) SetFeed(f *TranscriptFeed) {
	m.feed = f
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

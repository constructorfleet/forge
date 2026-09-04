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
}

// NewLiveModel builds a live roster model over r for executionID, polling
// every poll (defaulting to pollInterval when poll is not positive).
func NewLiveModel(r *Roster, executionID string, poll time.Duration) *LiveModel {
	if poll <= 0 {
		poll = pollInterval
	}
	return &LiveModel{Roster: r, ExecutionID: executionID, poll: poll}
}

// Init returns a command that paints the first frame immediately, then the
// poll loop takes over from the injected clock.
func (m *LiveModel) Init() tea.Cmd {
	return func() tea.Msg { return pollTickMsg{m.Roster.Now()} }
}

// Update drives the roster: a poll tick refetches state and schedules the
// next; q, Ctrl+C, or a programmatic interrupt quits the model. Quitting
// carries no stop-work signal.
func (m *LiveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case pollTickMsg:
		vm, err := m.Roster.Fetch(context.Background(), m.ExecutionID, msg.now)
		m.lastErr = err
		if err != nil {
			// A silent poll failure is indistinguishable from an idle roster.
			// Fetch returns a zero ViewModel on error, so hold the last good
			// rows: one transient read must not blank the frame for a tick.
			m.vm.Notice = err.Error()
		} else {
			// A poll refreshes the roster only. Pane attachment and focus are
			// the operator's, so a tick must never reset them.
			vm.Transcript, vm.Focus = m.vm.Transcript, m.vm.Focus
			m.vm = vm
		}
		return m, tea.Tick(m.poll, func(t time.Time) tea.Msg { return pollTickMsg{t} })
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

// SetTranscript attaches the transcript pane the frame renders. A nil pane
// renders the roster alone. Spec #488 wires the tailer's polled window and its
// scroller into the pane; until then only the model's tests attach one.
func (m *LiveModel) SetTranscript(p *TranscriptPane) {
	m.vm.Transcript = p
	if p == nil {
		m.vm.Focus = PaneRoster
	}
}

// View renders the current frame headless.
func (m *LiveModel) View() tea.View {
	return tea.NewView(Render(m.vm))
}

// Workers exposes the current roster rows, for the model's own tests.
func (m *LiveModel) Workers() []WorkerRow { return m.vm.Workers }

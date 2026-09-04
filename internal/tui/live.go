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
		m.vm, m.lastErr = m.Roster.Fetch(context.Background(), m.ExecutionID, msg.now)
		return m, tea.Tick(m.poll, func(t time.Time) tea.Msg { return pollTickMsg{t} })
	case tea.KeyPressMsg:
		if uv.Key(msg.Key()).MatchString("q", "ctrl+c") {
			return m, tea.Quit
		}
	case tea.InterruptMsg:
		// The TUI's own suspend signal binds to q too.
		return m, tea.Quit
	}
	return m, nil
}

// View renders the current roster frame headless.
func (m *LiveModel) View() tea.View {
	return tea.NewView(Render(m.vm))
}

// Workers exposes the current roster rows, for the model's own tests.
func (m *LiveModel) Workers() []WorkerRow { return m.vm.Workers }

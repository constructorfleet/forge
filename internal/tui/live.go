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

	// OpenDiff defers a diff to $PAGER, writing its artifact under the given
	// directory. Injected so a test drives the whole key path without spawning
	// a process; nil uses OpenDiffInPager.
	OpenDiff func(dir, diff string) tea.Cmd

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
			vm.ActionNotice = m.vm.ActionNotice
			m.vm = vm
		}
		return m, tea.Tick(m.poll, func(t time.Time) tea.Msg { return pollTickMsg{t} })
	case tea.KeyPressMsg:
		key := uv.Key(msg.Key())
		// Any key press answers the last action notice, so it clears here.
		m.vm.ActionNotice = ""
		if key.MatchString("q", "ctrl+c") {
			m.removeDiffArtifacts()
			return m, tea.Quit
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
	case tea.InterruptMsg:
		// The TUI's own suspend signal binds to q too.
		m.removeDiffArtifacts()
		return m, tea.Quit
	}
	return m, nil
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

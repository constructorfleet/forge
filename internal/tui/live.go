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

	// lastCommit is the clock time of the last transcript read that actually
	// committed to the pane or notice (a stale-retry never sets it). Zero
	// holds before the first commit. transcriptLagAge measures against it, so
	// a store slower than the poll interval — which transcriptController's
	// reading gates to one at a time — surfaces as a growing age instead of a
	// silently thinned refresh rate. PlanningModel keeps its own lastCommit
	// for the same reason, so the field stays out of the shared
	// transcriptController rather than serving both from one copy.
	lastCommit time.Time

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

	// diffKey identifies the Review the open diff pane summarizes: the Issue,
	// its verdict, and whether it stored a diff. A roster pass whose selected
	// row yields another key reloads the pane; an unchanged key reads nothing,
	// so an open pane costs no blob read per poll.
	diffKey string
	// diffLoading records a diff summary read in flight, so a poll cannot
	// start a second one while a slow store still reads.
	diffLoading bool
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
		vm:          ViewModel{Style: DefaultStyle(), PollInterval: poll, ExecutionID: executionID},
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
		// The lag age is resolved against this tick's own clock, so it grows
		// tick over tick while a slow store leaves the one in-flight read
		// outstanding (see transcriptController.reading), rather than only at
		// the read that eventually commits.
		m.vm.TranscriptLagAge = m.transcriptLagAge(msg.now)
		next := tea.Tick(m.poll, func(t time.Time) tea.Msg { return pollTickMsg{t} })
		return m, tea.Batch(m.readRoster(msg.now), next)
	case rosterReadMsg:
		m.applyRoster(msg)
		// The roster row count is part of the chrome, so a pass that changed the
		// rows changes the transcript height as well.
		m.applyTranscriptHeight()
		return m, tea.Batch(m.readTranscript(), m.refreshDiff())
	case tea.WindowSizeMsg:
		m.winHeight = msg.Height
		m.winWidth = msg.Width
		m.vm.Width = msg.Width
		m.applyTranscriptHeight()
	case transcriptReadMsg:
		return m, m.applyTranscript(msg)
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
		return m, m.handleKey(key)
	case diffNoticeMsg:
		m.vm.ActionNotice = msg.text
	case diffReadyMsg:
		return m, m.openDiff(msg.dir, msg.diff)
	case diffLoadedMsg:
		m.applyDiffLoaded(msg)
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
	// The roster refresh keeps the operator's pane, focus, and chosen row: a
	// sequential run's later Issues would otherwise vanish behind the first
	// row every time a poll pass replaced the view-model.
	vm.Transcript, vm.Focus = m.vm.Transcript, m.vm.Focus
	vm.ActionNotice = m.vm.ActionNotice
	vm.TranscriptNotice = m.vm.TranscriptNotice
	// The lag age and the poll interval it is measured against are both
	// resolved elsewhere (the pollTickMsg case above, and construction) rather
	// than by this roster refresh, so a fresh ViewModel's zero values must not
	// overwrite them here.
	vm.TranscriptLagAge = m.vm.TranscriptLagAge
	vm.PollInterval = m.vm.PollInterval
	// The refresh also keeps the operator's chosen Worker selected, clamped to
	// the new row count: a fresh ViewModel's Selection is always the zero
	// value, and copying it over would silently snap the pane back to the
	// first row on every poll.
	vm.Selection = m.vm.Selection
	if last := len(vm.Workers) - 1; vm.Selection > last {
		vm.Selection = last
	}
	if vm.Selection < 0 {
		vm.Selection = 0
	}
	// The agent selection, the diff pane, the terminal width, and the
	// Execution name are all operator or runtime state a fresh view-model
	// cannot know, so they carry over. The agent selection clamps to the
	// selected row's agents, which a poll can shrink.
	vm.AgentSelection = m.vm.AgentSelection
	if row, ok := selectedWorker(vm); ok {
		vm.AgentSelection = clampSelection(vm.AgentSelection, len(row.Agents))
	}
	vm.DiffOpen, vm.Diff, vm.DiffScroll = m.vm.DiffOpen, m.vm.Diff, m.vm.DiffScroll
	vm.Width, vm.ExecutionID = m.vm.Width, m.vm.ExecutionID
	// The colour scheme is set once at construction; a poll's fresh view-model
	// carries the zero Style, so copy it over or every poll would render plain.
	vm.Style = m.vm.Style
	m.vm = vm
}

// handleKey applies one non-quit key against the focused pane. The diff
// toggle and pane navigation act from every pane; every other key acts on
// the pane that holds focus alone, so a roster key and a pane key can share
// a rune without collision.
func (m *LiveModel) handleKey(key uv.Key) tea.Cmd {
	switch {
	case key.MatchString("d"):
		return m.toggleDiff()
	case key.MatchString("tab"):
		m.cycleFocus(1)
		return nil
	case key.MatchString("shift+tab", "backtab"):
		m.cycleFocus(-1)
		return nil
	}
	switch m.vm.Focus {
	case PaneRoster:
		return m.handleRosterKey(key)
	case PaneAgents:
		return m.handleAgentsKey(key)
	case PaneDiff:
		return m.handleDiffKey(key)
	default:
		m.handleTranscriptKey(key, m.vm.Transcript, &m.vm.Focus)
		return nil
	}
}

// handleRosterKey applies the execution list's keys: the Worker actions,
// row movement, and enter to inspect the output pane.
func (m *LiveModel) handleRosterKey(key uv.Key) tea.Cmd {
	switch {
	case key.MatchString("c"):
		return m.armCancelConfirm()
	case key.MatchString("r"):
		return m.startRetry()
	case key.MatchString("p"):
		return m.openSelectedApprove()
	case key.MatchString("a"):
		return m.openSelectedAnswer()
	case key.MatchString("enter"):
		m.inspectOutput()
		return nil
	}
	cmd, _ := m.moveRosterSelection(key)
	return cmd
}

// handleAgentsKey applies the agents pane's keys: agent movement, which
// re-filters the output pane at once, and enter to inspect it.
func (m *LiveModel) handleAgentsKey(key uv.Key) tea.Cmd {
	switch {
	case key.MatchString("j", "down"):
		m.moveAgentSelection(1)
	case key.MatchString("k", "up"):
		m.moveAgentSelection(-1)
	case key.MatchString("enter"):
		m.inspectOutput()
	}
	return nil
}

// handleDiffKey applies the diff pane's keys: file-list scrolling and enter
// to hand the whole diff to $PAGER.
func (m *LiveModel) handleDiffKey(key uv.Key) tea.Cmd {
	switch {
	case key.MatchString("j", "down"):
		m.scrollDiff(1)
	case key.MatchString("k", "up"):
		m.scrollDiff(-1)
	case key.MatchString("enter"):
		return m.openSelectedDiff()
	}
	return nil
}

// inspectOutput moves focus onto the output pane, when there is one.
func (m *LiveModel) inspectOutput() {
	if m.vm.Transcript != nil {
		m.vm.Focus = PaneTranscript
	}
}

// paneOrder lists the panes tab walks, in order, skipping any the frame does
// not draw right now: the agents pane needs a selected Worker, the output
// pane a transcript, the diff pane an open toggle.
func (m *LiveModel) paneOrder() []Pane {
	order := []Pane{PaneRoster}
	if _, ok := selectedWorker(m.vm); ok {
		order = append(order, PaneAgents)
	}
	if m.vm.Transcript != nil {
		order = append(order, PaneTranscript)
	}
	if m.vm.DiffOpen {
		order = append(order, PaneDiff)
	}
	return order
}

// cycleFocus moves focus delta panes along paneOrder, wrapping at both ends.
func (m *LiveModel) cycleFocus(delta int) {
	order := m.paneOrder()
	at := 0
	for i, p := range order {
		if p == m.vm.Focus {
			at = i
		}
	}
	n := len(order)
	m.vm.Focus = order[((at+delta)%n+n)%n]
}

// moveAgentSelection shifts the agent selection by delta, clamped to the
// selected Worker's agents, and re-filters the output pane to the agent now
// selected so the switch shows at once rather than after the next poll.
func (m *LiveModel) moveAgentSelection(delta int) {
	row, ok := selectedWorker(m.vm)
	if !ok {
		return
	}
	next := clampSelection(m.vm.AgentSelection+delta, len(row.Agents))
	if next == m.vm.AgentSelection {
		return
	}
	m.vm.AgentSelection = next
	m.syncAgentFilter()
}

// agentFilter returns the output pane's filter for the selected agent. The
// filter is always on: the output pane shows one agent at a time, the
// implementation Agent by default.
func (m *LiveModel) agentFilter() AgentFilter {
	a, ok := selectedAgent(m.vm)
	if !ok {
		return AgentFilter{Enabled: true}
	}
	return AgentFilter{Enabled: true, Subagent: a.Subagent}
}

// syncAgentFilter applies the selected agent's filter to the feed's pane for
// the selected Worker and shows the re-drawn pane. It runs after every
// committed transcript read as well as on an agent move, so a pane the feed
// built this poll picks the filter up at once.
func (m *LiveModel) syncAgentFilter() {
	if m.feed == nil {
		return
	}
	row, ok := selectedWorker(m.vm)
	if !ok {
		return
	}
	if pane := m.feed.SelectAgent(row.IssueID, m.agentFilter()); pane != nil {
		m.vm.Transcript = pane
	}
}

// scrollDiff moves the diff pane's first visible file by delta, clamped to
// the file list.
func (m *LiveModel) scrollDiff(delta int) {
	if m.vm.Diff == nil {
		return
	}
	m.vm.DiffScroll = clampSelection(m.vm.DiffScroll+delta, len(m.vm.Diff.Files))
}

// toggleDiff opens or closes the diff pane for the selected Worker. Opening
// starts the summary read; closing drops the summary and returns focus to
// the roster if the pane held it. With no stored diff the key explains
// itself instead of opening an empty pane.
func (m *LiveModel) toggleDiff() tea.Cmd {
	if m.vm.DiffOpen {
		m.vm.DiffOpen, m.vm.Diff, m.vm.DiffScroll = false, nil, 0
		m.diffKey = ""
		if m.vm.Focus == PaneDiff {
			m.vm.Focus = PaneRoster
		}
		return nil
	}
	row, ok := selectedWorker(m.vm)
	if !ok {
		m.vm.ActionNotice = "no Worker selected"
		return nil
	}
	if !row.HasDiff {
		m.vm.ActionNotice = fmt.Sprintf("no diff for %s yet", row.IssueID)
		return nil
	}
	m.vm.DiffOpen, m.vm.Diff, m.vm.DiffScroll = true, nil, 0
	return m.loadDiff(row)
}

// diffLoadedMsg carries a finished diff summary read back to the update loop.
type diffLoadedMsg struct {
	key     string
	summary DiffSummary
	err     error
}

// rowDiffKey names the Review a row's diff pane would summarize.
func rowDiffKey(row WorkerRow) string {
	return fmt.Sprintf("%s|%s|%t", row.IssueID, row.Verdict, row.HasDiff)
}

// loadDiff returns the command that reads row's diff and summarizes it. The
// read runs in the command, off the update goroutine, so a large blob cannot
// delay a key press. One read runs at a time.
func (m *LiveModel) loadDiff(row WorkerRow) tea.Cmd {
	if m.diffLoading {
		return nil
	}
	m.diffLoading = true
	key := rowDiffKey(row)
	m.diffKey = key
	store, executionID, issueID := m.Roster.Store, m.ExecutionID, row.IssueID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), diffReadTimeout)
		defer cancel()
		diff, err := LatestDiff(ctx, store, executionID, issueID)
		if err != nil {
			return diffLoadedMsg{key: key, err: err}
		}
		return diffLoadedMsg{key: key, summary: SummarizeDiff(diff)}
	}
}

// applyDiffLoaded commits a finished summary read to the open pane. A read
// for a Review the pane has since moved away from is dropped, and a read
// failure leaves the pane's last summary and reports the failure.
func (m *LiveModel) applyDiffLoaded(msg diffLoadedMsg) {
	m.diffLoading = false
	if !m.vm.DiffOpen || msg.key != m.diffKey {
		return
	}
	if msg.err != nil {
		if errors.Is(msg.err, ErrNoDiff) {
			m.vm.Diff = &DiffSummary{}
			return
		}
		m.vm.ActionNotice = msg.err.Error()
		return
	}
	summary := msg.summary
	m.vm.Diff = &summary
	m.vm.DiffScroll = clampSelection(m.vm.DiffScroll, len(summary.Files))
}

// refreshDiff reloads the open diff pane when the selected row's Review
// changed since the last read: another Issue, a new verdict, or a diff that
// appeared. An unchanged Review reads nothing.
func (m *LiveModel) refreshDiff() tea.Cmd {
	if !m.vm.DiffOpen {
		return nil
	}
	row, ok := selectedWorker(m.vm)
	if !ok {
		m.vm.DiffOpen, m.vm.Diff, m.diffKey = false, nil, ""
		if m.vm.Focus == PaneDiff {
			m.vm.Focus = PaneRoster
		}
		return nil
	}
	if rowDiffKey(row) == m.diffKey {
		return nil
	}
	if !row.HasDiff {
		m.vm.Diff, m.diffKey = &DiffSummary{}, rowDiffKey(row)
		return nil
	}
	m.vm.Diff = nil
	return m.loadDiff(row)
}

// moveSelection shifts the roster selection by delta, clamped to the
// current rows. It is a no-op on an empty roster.
func (m *LiveModel) moveSelection(delta int) {
	m.vm.Selection = clampSelection(m.vm.Selection+delta, len(m.vm.Workers))
}

// clampSelection bounds a roster selection to [0, n-1], or 0 for an empty
// roster.
func clampSelection(sel, n int) int {
	if n == 0 {
		return 0
	}
	if sel < 0 {
		return 0
	}
	if sel >= n {
		return n - 1
	}
	return sel
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

// moveRosterSelection applies j/k and up/down against the roster selection,
// so the operator can switch between the Workers of several Issues running
// concurrently under one Execution instead of being stuck on the first row.
// The move clamps at the first and last row rather than wrapping. It returns
// whether the key was a roster movement key at all, so an unrelated key still
// falls through to the transcript pane's own handler.
func (m *LiveModel) moveRosterSelection(key uv.Key) (tea.Cmd, bool) {
	var delta int
	switch {
	case key.MatchString("j", "down"):
		delta = 1
	case key.MatchString("k", "up"):
		delta = -1
	default:
		return nil, false
	}
	next := m.vm.Selection + delta
	if next < 0 {
		next = 0
	}
	if last := len(m.vm.Workers) - 1; next > last {
		next = last
	}
	if next == m.vm.Selection {
		return nil, true
	}
	m.vm.Selection = next
	// Another Worker has its own agents and its own diff, so the agent
	// selection and the file scroll start over. The diff pane, if open,
	// reloads for the new row.
	m.vm.AgentSelection, m.vm.DiffScroll = 0, 0
	// The selection just moved to another Issue: the transcript pane must
	// follow at once rather than waiting up to a full poll interval to show
	// the newly selected Worker's own context.
	return tea.Batch(m.readTranscript(), m.refreshDiff()), true
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
// TranscriptNotice, so a transient failure never blanks the transcript. A read
// for a Worker the operator has since moved away from is dropped instead: it
// starts a fresh read for the Worker now selected, so a stale read can never
// paint the wrong Worker's transcript into the pane.
func (m *LiveModel) applyTranscript(msg transcriptReadMsg) tea.Cmd {
	want := ""
	if row, ok := selectedWorker(m.vm); ok {
		want = row.IssueID
	}
	cmd, committed := m.transcriptController.applyTranscript(msg, want, &m.vm.TranscriptNotice, &m.vm.Transcript, m.readTranscript)
	if committed {
		m.lastCommit = m.Roster.Now()
		// The feed may have built the pane this pass, so it takes the
		// selected agent's filter here.
		m.syncAgentFilter()
	}
	return cmd
}

// transcriptLagAge returns the time since the last committed transcript read,
// measured against now. It is zero before the first commit: a pane that has
// not read yet is starting, not lagging.
func (m *LiveModel) transcriptLagAge(now time.Time) time.Duration {
	if m.lastCommit.IsZero() {
		return 0
	}
	return now.Sub(m.lastCommit)
}

// applyTranscriptHeight sizes the tailer's event window from the transcript row
// budget. The two units differ: the tailer counts events and the budget counts
// rows, and one event can draw several rows. So this is an upper bound on how
// much history to read, and Render owns the exact clip to the terminal. It runs
// on a resize, on each poll (the roster row count is part of the chrome), and
// when a feed is attached.
func (m *LiveModel) applyTranscriptHeight() {
	m.vm.Height = m.winHeight
	width := 0
	if m.winWidth > 0 {
		width = TranscriptWidth(m.vm)
	}
	m.sizeFeed(TranscriptRows(m.vm), width)
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

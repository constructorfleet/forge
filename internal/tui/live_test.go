package tui_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
	uv "github.com/charmbracelet/ultraviolet"
)

func liveFixture(t *testing.T, now time.Time) (*tui.LiveModel, *fakeRosterStore) {
	t.Helper()
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{
					ID: "#1", Title: "Write tests", State: domain.StateImplementing,
					StateChangedAt: now.Add(-30 * time.Second),
				},
			},
		},
		claimOK: map[string]bool{"#1": true},
		claims:  map[string]storage.WorkerClaim{"#1": {LastHeartbeat: now.Add(-3 * time.Second)}},
	}
	roster := tui.NewRoster(store, func() time.Time { return now })
	// A short interval keeps the tests off the wall clock: they drive every pass
	// by hand, and the clock the roster reads is injected.
	return tui.NewLiveModel(roster, "ex-1", time.Millisecond), store
}

// nextPollTick drives one whole poll cycle through the real Init command: the
// tick, then every command it batched, which is how the runtime delivers the
// feed read. It returns the updated model and the command that schedules the
// following tick.
func nextPollTick(t *testing.T, m *tui.LiveModel) (tea.Model, tea.Cmd) {
	t.Helper()
	cmd := m.Init()
	if cmd == nil {
		t.Fatalf("Init returned nil cmd")
	}
	next, nextCmd := m.Update(cmd())
	if nextCmd == nil {
		t.Fatalf("a poll tick must schedule the next tick")
	}
	tick, ok := drainBatch(t, m, nextCmd)
	if !ok {
		t.Fatalf("a poll tick must schedule the next tick")
	}
	return next, tick
}

// drainBatch delivers the messages a poll tick's batch produces and returns the
// scheduled tick, which it never invokes: the tick sleeps for the poll interval.
// The model batches the tick last, and a bare command is the tick alone.
func drainBatch(t *testing.T, m *tui.LiveModel, cmd tea.Cmd) (tea.Cmd, bool) {
	t.Helper()
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		return cmd, true
	}
	for _, c := range batch[:len(batch)-1] {
		runImmediateCmd(t, m, c)
	}
	return batch[len(batch)-1], true
}

// runImmediateCmd delivers a command and every command the model returns from
// that message. It is for commands that do store work now; it must not receive
// the sleeping tick command.
func runImmediateCmd(t *testing.T, m *tui.LiveModel, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			runImmediateCmd(t, m, c)
		}
		return
	}
	_, next := m.Update(msg)
	runImmediateCmd(t, m, next)
}

// TestLiveModelPollTickFetchesAndRenders proves a poll tick resolves state
// into the frame and schedules the next tick.
func TestLiveModelPollTickFetchesAndRenders(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)

	next, _ := nextPollTick(t, m)
	lm, ok := next.(*tui.LiveModel)
	if !ok {
		t.Fatalf("Update returned %T, want *tui.LiveModel", next)
	}
	if len(lm.Workers()) != 1 {
		t.Fatalf("len(Workers()) = %d, want 1", len(lm.Workers()))
	}
	if len(lm.View().Content) == 0 {
		t.Fatal("View rendered empty content after a poll tick")
	}
}

// TestLiveModelViewRunsInAltScreen proves the live view claims the terminal's
// alternate screen buffer, so a frame taller than the last never scrolls
// earlier frames above the visible window: the terminal redraws the whole
// screen from a fixed top rather than appending output.
func TestLiveModelViewRunsInAltScreen(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)

	if !m.View().AltScreen {
		t.Fatal("View().AltScreen = false, want true")
	}
}

// TestLiveModelQuitKeyLeavesExecutionRunning proves q quits the model (the
// Bubble Tea run returns) but does not signal any stop-work: the model has no
// path to cancel an Execution, so quitting is always safe.
func TestLiveModelQuitKeyLeavesExecutionRunning(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "q", Code: 'q'}))
	assertQuitCmd(t, cmd)
}

// TestLiveModelCtrlCQuitsLikeQ proves Ctrl+C binds to q: the model quits and
// there is no stop-work side channel.
func TestLiveModelCtrlCQuitsLikeQ(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Mod: uv.ModCtrl, Code: 'c'}))
	assertQuitCmd(t, cmd)
}

// TestLiveModelOtherKeysDoNotQuit proves an unrelated key leaves the model
// running (no quit command).
func TestLiveModelOtherKeysDoNotQuit(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
	if cmd != nil {
		t.Fatalf("unrelated key returned cmd %v, want nil", cmd)
	}
}

// TestLiveModelInterruptMsgQuits proves a programmatic InterruptMsg (the
// TUI's suspend signal) also quits cleanly.
func TestLiveModelInterruptMsgQuits(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)

	_, cmd := m.Update(tea.InterruptMsg{})
	assertQuitCmd(t, cmd)
}

// press sends one key to the model and returns the rendered frame. Named keys
// carry their own key code; anything else is a single text rune.
func press(t *testing.T, m *tui.LiveModel, name string) string {
	t.Helper()
	key := tea.Key{Text: name, Code: rune(name[0])}
	switch name {
	case "tab":
		key = tea.Key{Code: uv.KeyTab}
	case "enter":
		key = tea.Key{Code: uv.KeyEnter}
	case "up":
		key = tea.Key{Code: uv.KeyUp}
	case "down":
		key = tea.Key{Code: uv.KeyDown}
	case "pgup":
		key = tea.Key{Code: uv.KeyPgUp}
	case "pgdown":
		key = tea.Key{Code: uv.KeyPgDown}
	}
	m.Update(tea.KeyPressMsg(key))
	return m.View().Content
}

// transcriptModel builds a live model whose polled transcript pane holds one
// prose event and one tool call, with focus still on the roster.
func transcriptModel(t *testing.T) *tui.LiveModel {
	t.Helper()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	m.SetFeed(tui.NewTranscriptFeed(feedFixture()))
	nextPollTick(t, m)
	return m
}

// TestLiveModelTabFocusesTranscript proves tab moves focus onto the pane, so
// the footer offers the transcript keys and no Worker key.
func TestLiveModelTabFocusesTranscript(t *testing.T) {
	m := transcriptModel(t)

	got := press(t, m, "tab")
	if !strings.Contains(visible(got), "[tab] roster") {
		t.Errorf("footer is not the transcript footer after tab:\n%s", got)
	}
	if strings.Contains(visible(got), "[c] cancel") {
		t.Errorf("footer keeps a Worker key while the transcript has focus:\n%s", got)
	}

	got = press(t, m, "tab")
	if !strings.Contains(visible(got), "[c] cancel") {
		t.Errorf("tab did not return focus to the roster:\n%s", got)
	}
}

// TestLiveModelEnterExpandsSelectedToolCall proves the expand key reaches the
// pane and that a selection move collapses again.
func TestLiveModelEnterExpandsSelectedToolCall(t *testing.T) {
	m := transcriptModel(t)
	// A fresh pane selects the newest entry: the tool call.
	press(t, m, "tab")

	if got := press(t, m, "enter"); !strings.Contains(got, "go build ./...") {
		t.Fatalf("enter did not expand the selected tool call:\n%s", got)
	}
	if got := press(t, m, "k"); strings.Contains(got, "go build ./...") {
		t.Errorf("a selection move left the entry expanded:\n%s", got)
	}
	if !strings.Contains(press(t, m, "j"), "seq 1") {
		t.Errorf("selection move did not update the detail strip")
	}
}

// TestLiveModelFollowTailKeyReachesPane proves the footer's follow-tail key has
// a handler on a polled pane, so the footer never advertises an inert key, and
// that the key returns the selection to the tail and then clears itself.
func TestLiveModelFollowTailKeyReachesPane(t *testing.T) {
	m := transcriptModel(t)
	press(t, m, "tab")
	// One selection move pins the pane, which is what the follow-tail key undoes.
	if got := press(t, m, "k"); !strings.Contains(visible(got), "[G] follow tail") {
		t.Fatalf("footer omits the follow-tail key while the selection is pinned:\n%s", got)
	}

	got := press(t, m, "G")
	if !strings.Contains(got, "seq 1") {
		t.Errorf("the G key did not return the selection to the tail:\n%s", got)
	}
	if strings.Contains(visible(got), "[G] follow tail") {
		t.Errorf("footer still offers follow tail after the pane followed it:\n%s", got)
	}
}

// manyEventsFeedStore builds a store whose one AgentRun carries more prose
// events than the tailer's default scrollback height (20), so the
// tail-following window always hides the oldest event.
func manyEventsFeedStore() *fakeFeedStore {
	events := make([]storage.TranscriptEvent, 0, 25)
	for i := 0; i < 25; i++ {
		events = append(events, storage.TranscriptEvent{
			AgentRunID: 7, Seq: i, Type: "MESSAGE", Role: "assistant",
			Text: fmt.Sprintf("event %d", i),
		})
	}
	return &fakeFeedStore{
		runs:   map[string][]storage.AgentRun{"#1": {{ID: 7, ExecutionID: "ex-1", IssueID: "#1"}}},
		events: map[int64][]storage.TranscriptEvent{7: events},
	}
}

// TestLiveModelPageKeysReachThePane proves pgup and pgdown reach the
// transcript pane's window-anchor seam: a scrolled-back window reveals an
// event the tail-following default height hides, and pgdown returns it.
func TestLiveModelPageKeysReachThePane(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	m.SetFeed(tui.NewTranscriptFeed(manyEventsFeedStore()))
	nextPollTick(t, m)
	if got := m.View().Content; strings.Contains(got, "event 0\n") || strings.Contains(got, "event 0 ") {
		t.Fatalf("the tail-following window already shows the oldest event:\n%s", got)
	}
	press(t, m, "tab")

	press(t, m, "pgup")
	nextPollTick(t, m)
	if got := m.View().Content; !strings.Contains(got, "event 0") {
		t.Fatalf("pgup did not scroll the window back to the oldest event:\n%s", got)
	}

	press(t, m, "pgdown")
	nextPollTick(t, m)
	if got := m.View().Content; strings.Contains(got, "event 0") {
		t.Errorf("pgdown did not return the window to the tail:\n%s", got)
	}
}

// TestLiveModelRosterKeysMoveSelectionBetweenWorkers proves j/k (and up/down)
// move the roster selection between concurrently running Workers, so the
// operator can switch between them instead of being stuck on the first row.
func TestLiveModelRosterKeysMoveSelectionBetweenWorkers(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "First issue", State: domain.StateImplementing, StateChangedAt: now},
				{ID: "#2", Title: "Second issue", State: domain.StateReviewing, StateChangedAt: now},
				{ID: "#3", Title: "Third issue", State: domain.StateValidating, StateChangedAt: now},
			},
		},
	}
	roster := tui.NewRoster(store, func() time.Time { return now })
	m := tui.NewLiveModel(roster, "ex-1", time.Millisecond)
	nextPollTick(t, m)

	if got := m.View().Content; !strings.Contains(got, "IMPLEMENTING |") {
		t.Fatalf("fresh roster does not select the first Worker:\n%s", got)
	}

	if got := press(t, m, "j"); !strings.Contains(got, "REVIEWING |") {
		t.Errorf("j did not move the selection to the second Worker:\n%s", got)
	}
	if got := press(t, m, "j"); !strings.Contains(got, "VALIDATING |") {
		t.Errorf("a second j did not move the selection to the third Worker:\n%s", got)
	}
	// The selection holds at the last row: it does not wrap.
	if got := press(t, m, "j"); !strings.Contains(got, "VALIDATING |") {
		t.Errorf("j past the last Worker moved the selection:\n%s", got)
	}
	if got := press(t, m, "k"); !strings.Contains(got, "REVIEWING |") {
		t.Errorf("k did not move the selection back to the second Worker:\n%s", got)
	}
	if got := press(t, m, "up"); !strings.Contains(got, "IMPLEMENTING |") {
		t.Errorf("up did not move the selection back to the first Worker:\n%s", got)
	}
	// The selection holds at the first row: it does not wrap.
	if got := press(t, m, "up"); !strings.Contains(got, "IMPLEMENTING |") {
		t.Errorf("up past the first Worker moved the selection:\n%s", got)
	}
	if got := press(t, m, "down"); !strings.Contains(got, "REVIEWING |") {
		t.Errorf("down did not move the selection to the second Worker:\n%s", got)
	}
}

// TestLiveModelRosterSelectionSurvivesPoll proves a poll tick after a j/k move
// keeps the operator's chosen Worker selected, so the switch does not revert
// to the first row on its own within the next poll interval.
func TestLiveModelRosterSelectionSurvivesPoll(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "First issue", State: domain.StateImplementing, StateChangedAt: now},
				{ID: "#2", Title: "Second issue", State: domain.StateReviewing, StateChangedAt: now},
				{ID: "#3", Title: "Third issue", State: domain.StateValidating, StateChangedAt: now},
			},
		},
	}
	roster := tui.NewRoster(store, func() time.Time { return now })
	m := tui.NewLiveModel(roster, "ex-1", time.Millisecond)
	nextPollTick(t, m)

	if got := press(t, m, "j"); !strings.Contains(got, "REVIEWING |") {
		t.Fatalf("j did not move the selection to the second Worker:\n%s", got)
	}

	nextPollTick(t, m)

	if got := m.View().Content; !strings.Contains(got, "REVIEWING |") {
		t.Errorf("a poll tick after j reverted the selection instead of keeping the second Worker selected:\n%s", got)
	}
}

// TestLiveModelTranscriptKeysDoNothingOnRosterFocus proves the pane keys act
// only while the pane holds focus.
func TestLiveModelTranscriptKeysDoNothingOnRosterFocus(t *testing.T) {
	m := transcriptModel(t)

	if got := press(t, m, "enter"); strings.Contains(got, "go build ./...") {
		t.Errorf("enter expanded the pane while the roster had focus:\n%s", got)
	}
}

// TestLiveModelTabWithoutTranscriptStaysOnRoster proves focus never moves to a
// pane that does not exist.
func TestLiveModelTabWithoutTranscriptStaysOnRoster(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	nextPollTick(t, m)

	if got := press(t, m, "tab"); !strings.Contains(visible(got), "[c] cancel") {
		t.Errorf("footer left the roster with no transcript pane:\n%s", got)
	}
}

// TestLiveModelPollDrivesTranscriptFeed proves an attached feed reaches the
// frame: one poll tick renders the selected Worker's events and gate rows,
// with no pane built by the test.
func TestLiveModelPollDrivesTranscriptFeed(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	m.SetFeed(tui.NewTranscriptFeed(feedFixture()))

	nextPollTick(t, m)

	got := m.View().Content
	for _, want := range []string{"starting work", "gate go-test (fail, exit 1)"} {
		if !strings.Contains(got, want) {
			t.Errorf("frame omits %q after a poll tick:\n%s", want, got)
		}
	}
}

// TestLiveModelFeedKeysReachThePolledPane proves the pane a poll built takes
// the transcript keys, so the wiring produces a usable pane and not a static
// render.
func TestLiveModelFeedKeysReachThePolledPane(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	m.SetFeed(tui.NewTranscriptFeed(feedFixture()))
	nextPollTick(t, m)

	press(t, m, "tab")
	if got := press(t, m, "enter"); !strings.Contains(got, "go build ./...") {
		t.Errorf("enter did not expand the polled pane's tool call:\n%s", got)
	}
}

// TestLiveModelFeedPollErrorKeepsFrame proves a failed feed read reports its
// notice and keeps the retained events, so a transient failure never blanks
// the pane.
func TestLiveModelFeedPollErrorKeepsFrame(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	store := feedFixture()
	m.SetFeed(tui.NewTranscriptFeed(store))
	nextPollTick(t, m)

	store.tailErr = errors.New("db is busy")
	nextPollTick(t, m)

	got := m.View().Content
	if !strings.Contains(got, "starting work") {
		t.Errorf("a failed feed poll blanked the pane:\n%s", got)
	}
	if !strings.Contains(got, "db is busy") {
		t.Errorf("frame hides the feed read failure:\n%s", got)
	}
}

// TestLiveModelWithoutFeedRendersRosterAlone proves the feed stays optional.
func TestLiveModelWithoutFeedRendersRosterAlone(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)

	nextPollTick(t, m)

	if got := m.View().Content; !strings.Contains(visible(got), "[c] cancel") {
		t.Errorf("frame left the roster with no feed attached:\n%s", got)
	}
}

// TestLiveModelBothPollFailuresRender proves a roster failure and a feed
// failure in one pass both reach the frame: neither notice hides the other, so
// the operator learns the rows are stale as well as the transcript.
func TestLiveModelBothPollFailuresRender(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, rosterStore := liveFixture(t, now)
	feedStore := feedFixture()
	m.SetFeed(tui.NewTranscriptFeed(feedStore))
	nextPollTick(t, m)

	rosterStore.loadErr = errors.New("roster is unreachable")
	feedStore.tailErr = errors.New("db is busy")
	nextPollTick(t, m)

	got := m.View().Content
	for _, want := range []string{"roster is unreachable", "db is busy"} {
		if !strings.Contains(got, want) {
			t.Errorf("frame omits %q when both reads fail:\n%s", want, got)
		}
	}
}

// TestLiveModelSetFeedDetachesTheOldPane proves the feed is the pane's one
// owner: attaching another feed drops the pane the earlier one built, so no
// stale transcript outlives its feed.
func TestLiveModelSetFeedDetachesTheOldPane(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	m.SetFeed(tui.NewTranscriptFeed(feedFixture()))
	nextPollTick(t, m)

	replacement := feedFixture()
	replacement.events[7] = []storage.TranscriptEvent{
		{AgentRunID: 7, Seq: 0, Type: "MESSAGE", Role: "assistant", Text: "second feed"},
	}
	m.SetFeed(tui.NewTranscriptFeed(replacement))
	if got := m.View().Content; strings.Contains(got, "starting work") {
		t.Errorf("the replaced feed's pane still renders:\n%s", got)
	}

	nextPollTick(t, m)
	if got := m.View().Content; !strings.Contains(got, "second feed") {
		t.Errorf("the new feed does not drive the pane:\n%s", got)
	}
}

// TestLiveModelKeyPressServedDuringASlowRead proves the store read runs off the
// update goroutine: a key press resolves while a feed read is still in flight.
func TestLiveModelKeyPressServedDuringASlowRead(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	store := feedFixture()
	release := make(chan struct{})
	store.block = release
	m.SetFeed(tui.NewTranscriptFeed(store))

	// Start the pass, then hold the feed read open by not draining it yet.
	_, cmd := m.Update(pollTick(t, m))
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("poll tick returned no roster command")
	}
	_, readCmd := m.Update(batch[0]())
	if readCmd == nil {
		t.Fatal("roster read returned no feed command")
	}
	read := make(chan tea.Msg, 1)
	go func() { read <- readCmd() }()

	_, quit := m.Update(tea.KeyPressMsg(tea.Key{Text: "q", Code: 'q'}))
	assertQuitCmd(t, quit)

	close(release)
	select {
	case <-read:
	case <-time.After(2 * time.Second):
		t.Fatal("the feed read never returned")
	}
}

// TestLiveModelStaleTranscriptReadDroppedOnSelectionChange proves a feed read
// started for one Worker, still in flight when the operator selects another
// Worker, never lands in the pane after the operator has moved on. Without
// this, the roster highlights the new Worker while the pane briefly shows the
// old one's transcript: the "runs bleed together" symptom this issue tracks.
func TestLiveModelStaleTranscriptReadDroppedOnSelectionChange(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "First issue", State: domain.StateImplementing, StateChangedAt: now},
				{ID: "#2", Title: "Second issue", State: domain.StateReviewing, StateChangedAt: now},
			},
		},
	}
	roster := tui.NewRoster(store, func() time.Time { return now })
	m := tui.NewLiveModel(roster, "ex-1", time.Millisecond)

	feedStore := feedFixture()
	feedStore.runs["#2"] = []storage.AgentRun{{ID: 8, ExecutionID: "ex-1", IssueID: "#2"}}
	feedStore.events[8] = []storage.TranscriptEvent{
		{AgentRunID: 8, Seq: 0, Type: "MESSAGE", Role: "assistant", Text: "second worker events"},
	}
	m.SetFeed(tui.NewTranscriptFeed(feedStore))

	// Start a pass, then hold the first Worker's feed read open by not
	// draining it yet.
	release := make(chan struct{})
	feedStore.block = release
	_, cmd := m.Update(pollTick(t, m))
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("poll tick returned no roster command")
	}
	_, readCmd := m.Update(batch[0]())
	if readCmd == nil {
		t.Fatal("roster read returned no feed command")
	}
	read := make(chan tea.Msg, 1)
	go func() { read <- readCmd() }()

	// The operator moves to the second Worker while the first Worker's read
	// is still in flight. A read already running, moveRosterSelection starts
	// no second one.
	m.Update(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))

	close(release)
	var msg tea.Msg
	select {
	case msg = <-read:
	case <-time.After(2 * time.Second):
		t.Fatal("the feed read never returned")
	}
	_, next := m.Update(msg)
	runImmediateCmd(t, m, next)

	got := m.View().Content
	if strings.Contains(got, "starting work") {
		t.Errorf("the stale read for the first Worker landed in the pane after the operator moved on:\n%s", got)
	}
	if !strings.Contains(got, "second worker events") {
		t.Errorf("the pane never picked up the now-selected Worker's own transcript:\n%s", got)
	}
}

// TestLiveModelKeyPressServedDuringASlowRosterFetch proves the roster store
// read runs off the update goroutine. A poll tick returns a command while the
// store is still blocked, so a key press can quit the model.
func TestLiveModelKeyPressServedDuringASlowRosterFetch(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, store := liveFixture(t, now)
	release := make(chan struct{})
	store.blockLoad = release

	done := make(chan tea.Cmd, 1)
	go func() {
		_, cmd := m.Update(pollTick(t, m))
		done <- cmd
	}()

	var cmd tea.Cmd
	select {
	case cmd = <-done:
	case <-time.After(200 * time.Millisecond):
		close(release)
		t.Fatal("poll tick blocked on the roster fetch")
	}
	if cmd == nil {
		close(release)
		t.Fatal("poll tick returned nil cmd, want read and tick commands")
	}

	_, quit := m.Update(tea.KeyPressMsg(tea.Key{Text: "q", Code: 'q'}))
	assertQuitCmd(t, quit)

	close(release)
}

// TestLiveModelOnlyOneReadInFlight proves a tick starts no second read while one
// is outstanding. Two reads share the feed's one tailer, whose cursors advance
// only at Apply, so the second would read and append the same events twice.
func TestLiveModelOnlyOneReadInFlight(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	m.SetFeed(tui.NewTranscriptFeed(feedFixture()))

	// Two ticks with no read committed between them: only the first may read.
	if reads := countReads(t, m); reads != 1 {
		t.Fatalf("the first tick started %d reads, want 1", reads)
	}
	if reads := countReads(t, m); reads != 0 {
		t.Errorf("a tick started %d reads while one was in flight, want 0", reads)
	}
}

// TestLiveModelMarksHeaderWhenTranscriptReadOutlivesPoll proves a store slower
// than the poll interval — which leaves the one in-flight read outstanding
// across several ticks (see TestLiveModelOnlyOneReadInFlight) — surfaces in
// the frame once its age passes the lag threshold, rather than only thinning
// the refresh rate with no signal to the operator.
func TestLiveModelMarksHeaderWhenTranscriptReadOutlivesPoll(t *testing.T) {
	clock := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "Write tests", State: domain.StateImplementing},
			},
		},
		claimOK: map[string]bool{"#1": true},
		claims:  map[string]storage.WorkerClaim{"#1": {LastHeartbeat: clock}},
	}
	roster := tui.NewRoster(store, func() time.Time { return clock })
	poll := time.Second
	m := tui.NewLiveModel(roster, "ex-1", poll)
	m.SetFeed(tui.NewTranscriptFeed(feedFixture()))

	// The first tick commits a read: the pane shows real content and the model
	// records the commit at clock.
	nextPollTick(t, m)
	if got := m.View().Content; !strings.Contains(got, "starting work") {
		t.Fatalf("the first tick did not commit a transcript read:\n%s", got)
	}

	// The second tick starts a read it never finishes (the returned command is
	// never invoked), mirroring a store slower than the poll interval. Every
	// later tick's readTranscript then finds one already in flight and starts
	// none of its own (see live.go's in-flight guard), so the age keeps
	// growing against the first tick's commit alone.
	cmd := m.Init()
	_, cmd2 := m.Update(cmd())
	batch, ok := cmd2().(tea.BatchMsg)
	if !ok {
		t.Fatal("a poll tick must schedule the read and the next tick")
	}
	if _, next := m.Update(batch[0]()); next == nil {
		t.Fatal("the roster read did not start a transcript read")
	}

	if got := m.View().Content; strings.Contains(got, "lagging") {
		t.Fatalf("the frame marks lag before the outstanding read ages past the threshold:\n%s", got)
	}

	clock = clock.Add(4 * poll)
	nextPollTick(t, m)

	if got := m.View().Content; !strings.Contains(got, "lagging") {
		t.Fatalf("the frame never marks the pane header once the read has outlived several polls:\n%s", got)
	}
}

// countReads runs one poll tick and counts the transcript reads its batch holds.
func countReads(t *testing.T, m *tui.LiveModel) int {
	t.Helper()
	_, cmd := m.Update(pollTick(t, m))
	if cmd == nil {
		t.Fatal("a poll tick must schedule the next tick")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		// The tick alone: no read started.
		return 0
	}
	for _, c := range batch[:len(batch)-1] {
		_, next := m.Update(c())
		if next != nil {
			return 1
		}
	}
	return 0
}

// pollTick returns the model's own first poll-tick message.
func pollTick(t *testing.T, m *tui.LiveModel) tea.Msg {
	t.Helper()
	cmd := m.Init()
	if cmd == nil {
		t.Fatalf("Init returned nil cmd")
	}
	return cmd()
}

// TestLiveModelFeedDetachesPaneWithoutSelection proves a roster that loses its
// rows also drops the transcript, so no pane outlives the Worker it describes.
func TestLiveModelFeedDetachesPaneWithoutSelection(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, rosterStore := liveFixture(t, now)
	m.SetFeed(tui.NewTranscriptFeed(feedFixture()))
	nextPollTick(t, m)
	press(t, m, "tab")

	rosterStore.state.Issues = nil
	nextPollTick(t, m)

	got := m.View().Content
	if strings.Contains(got, "starting work") {
		t.Errorf("the pane outlived its Worker row:\n%s", got)
	}
	if !strings.Contains(visible(got), "[q] quit") {
		t.Errorf("focus stayed on the detached pane:\n%s", got)
	}
}

// twoIssueLiveFixture builds a live model over two Issues: the first DONE
// (a finished sequential run), the second FAILED (an active run awaiting
// retry). It reproduces the sequential-execute shape where one Execution
// carries several Issues, so a test can prove the roster selection can
// reach either one.
func twoIssueLiveFixture(t *testing.T, now time.Time) (*tui.LiveModel, *fakeRosterStore) {
	t.Helper()
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "First run", State: domain.StateDone, StateChangedAt: now.Add(-time.Minute)},
				{ID: "#2", Title: "Second run", State: domain.StateFailed, StateChangedAt: now.Add(-time.Second)},
			},
		},
	}
	roster := tui.NewRoster(store, func() time.Time { return now })
	m := tui.NewLiveModel(roster, "ex-1", time.Millisecond)
	nextPollTick(t, m)
	return m, store
}

// TestLiveModelDownKeyMovesRosterSelection proves j/down moves the roster
// selection onto the next row, so the operator is not pinned to the first
// Issue once it reaches DONE.
func TestLiveModelDownKeyMovesRosterSelection(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := twoIssueLiveFixture(t, now)

	if got := m.View().Content; !strings.Contains(got, "DONE") {
		t.Fatalf("selection did not start on the first row:\n%s", got)
	}

	got := press(t, m, "j")
	if !strings.Contains(got, "FAILED") {
		t.Errorf("j did not move the selection to the second row:\n%s", got)
	}
	if !strings.Contains(visible(got), "[r] retry") {
		t.Errorf("footer omits the second row's legal key after the move:\n%s", got)
	}
}

// TestLiveModelUpKeyMovesRosterSelectionBack proves k/up moves the selection
// back toward the first row.
func TestLiveModelUpKeyMovesRosterSelectionBack(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := twoIssueLiveFixture(t, now)
	press(t, m, "j")

	got := press(t, m, "k")
	if !strings.Contains(got, "DONE") {
		t.Errorf("k did not move the selection back to the first row:\n%s", got)
	}
}

// TestLiveModelRosterSelectionClampsAtEnds proves the selection cannot walk
// past either end of the roster.
func TestLiveModelRosterSelectionClampsAtEnds(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := twoIssueLiveFixture(t, now)

	if got := press(t, m, "k"); !strings.Contains(got, "DONE") {
		t.Errorf("k at the first row moved the selection:\n%s", got)
	}

	press(t, m, "j")
	if got := press(t, m, "j"); !strings.Contains(got, "FAILED") {
		t.Errorf("j at the last row moved the selection:\n%s", got)
	}
}

// TestLiveModelFooterAdvertisesRosterNavigationWithSeveralRows proves the
// footer tells the operator j/k moves between runs once more than one row
// exists, so the key is discoverable and not just an unadvertised binding.
func TestLiveModelFooterAdvertisesRosterNavigationWithSeveralRows(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := twoIssueLiveFixture(t, now)

	if got := m.View().Content; !strings.Contains(visible(got), "[j/k] switch worker") {
		t.Errorf("footer omits the roster navigation key with two rows:\n%s", got)
	}
}

// TestLiveModelFooterOmitsRosterNavigationWithOneRow proves the hint stays
// away when there is nothing to switch to.
func TestLiveModelFooterOmitsRosterNavigationWithOneRow(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	nextPollTick(t, m)

	if got := m.View().Content; strings.Contains(visible(got), "[j/k] switch worker") {
		t.Errorf("footer offers roster navigation with a single row:\n%s", got)
	}
}

// TestLiveModelRosterSelectionSurvivesAPoll proves a poll tick keeps the
// operator's chosen row selected instead of resetting to the first row, so a
// sequential run's later Issues stay visible once picked.
func TestLiveModelRosterSelectionSurvivesAPoll(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := twoIssueLiveFixture(t, now)
	press(t, m, "j")

	nextPollTick(t, m)

	if got := m.View().Content; !strings.Contains(visible(got), "[r] retry") {
		t.Errorf("a poll tick reset the roster selection:\n%s", got)
	}
}

// assertQuitCmd invokes the quit command and asserts it yields the program
// exit message.
func assertQuitCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatalf("expected a quit command, got nil")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("quit command produced %T, want tea.QuitMsg", cmd())
	}
}

// TestLiveModelWindowSizeSetsTranscriptHeight proves the terminal size reaches
// the feed's tailer: the frame keeps the roster rows, the detail strip, and the
// footer, and the transcript takes the rows that remain.
func TestLiveModelWindowSizeSetsTranscriptHeight(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	m.SetFeed(tui.NewTranscriptFeed(feedFixture()))

	// One roster row, one detail strip, one footer: a height of 5 leaves 2 rows.
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 5})
	nextPollTick(t, m)

	got := m.View().Content
	if strings.Contains(got, "starting work") {
		t.Errorf("the transcript ignored the terminal height:\n%s", got)
	}
	// The gate row is the newest entry, so it is what the two rows must hold.
	if !strings.Contains(got, "gate go-test") {
		t.Errorf("frame omits the newest entries:\n%s", got)
	}
}

// TestLiveModelWindowSizeSetsTranscriptWidth proves the terminal width reaches
// the feed's pane: a narrow terminal wraps the transcript's lines to more rows
// than a wide one, so a line longer than the terminal no longer overflows the
// terminal below the footer. The height is large enough that neither render
// clips, so the row-count difference comes from wrapping alone.
func TestLiveModelWindowSizeSetsTranscriptWidth(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	m, _ := liveFixture(t, now)
	m.SetFeed(tui.NewTranscriptFeed(feedFixture()))
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 100})
	nextPollTick(t, m)
	wide := len(splitLines(m.View().Content))

	m2, _ := liveFixture(t, now)
	m2.SetFeed(tui.NewTranscriptFeed(feedFixture()))
	m2.Update(tea.WindowSizeMsg{Width: 5, Height: 100})
	nextPollTick(t, m2)
	narrow := len(splitLines(m2.View().Content))

	if narrow <= wide {
		t.Errorf("a width of 5 produced %d rows, want more than the width-200 render's %d", narrow, wide)
	}
}

// TestLiveModelWindowSizeAfterPollResizesTranscript proves a resize after the
// first poll reaches the pane the feed already holds.
func TestLiveModelWindowSizeAfterPollResizesTranscript(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	m.SetFeed(tui.NewTranscriptFeed(feedFixture()))
	nextPollTick(t, m)
	if got := m.View().Content; !strings.Contains(got, "starting work") {
		t.Fatalf("the default height dropped the oldest event:\n%s", got)
	}

	m.Update(tea.WindowSizeMsg{Width: 80, Height: 5})
	nextPollTick(t, m)

	if got := m.View().Content; strings.Contains(got, "starting work") {
		t.Errorf("a resize did not shrink the transcript:\n%s", got)
	}
}

// TestLiveModelTinyWindowKeepsOneTranscriptRow proves a terminal too short for
// the chrome still renders one transcript row, and never falls back to the
// 20-event default.
func TestLiveModelTinyWindowKeepsOneTranscriptRow(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	m.SetFeed(tui.NewTranscriptFeed(feedFixture()))

	m.Update(tea.WindowSizeMsg{Width: 80, Height: 1})
	nextPollTick(t, m)

	got := strings.Split(strings.TrimRight(m.View().Content, "\n"), "\n")
	if strings.Contains(m.View().Content, "starting work") {
		t.Errorf("a one-row terminal rendered the whole scrollback:\n%s", m.View().Content)
	}
	// The chrome alone takes three rows, so exactly one transcript row remains.
	if len(got) != 4 {
		t.Errorf("frame drew %d rows, want the chrome plus one:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// TestLiveModelWindowSizeClipsTheFrame proves the terminal height reaches the
// frame: the whole render fits the rows the terminal has.
func TestLiveModelWindowSizeClipsTheFrame(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	m.SetFeed(tui.NewTranscriptFeed(feedFixture()))

	m.Update(tea.WindowSizeMsg{Width: 80, Height: 6})
	nextPollTick(t, m)

	got := strings.Split(strings.TrimRight(m.View().Content, "\n"), "\n")
	if len(got) > 6 {
		t.Errorf("frame drew %d rows in a 6-row terminal:\n%s", len(got), strings.Join(got, "\n"))
	}
	if !strings.Contains(m.View().Content, "gate go-test") {
		t.Errorf("clipping dropped the newest rows:\n%s", m.View().Content)
	}
}

package tui_test

import (
	"errors"
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
		m.Update(c())
	}
	return batch[len(batch)-1], true
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
	if !strings.Contains(got, "[tab] roster") {
		t.Errorf("footer is not the transcript footer after tab:\n%s", got)
	}
	if strings.Contains(got, "[c] cancel") {
		t.Errorf("footer keeps a Worker key while the transcript has focus:\n%s", got)
	}

	got = press(t, m, "tab")
	if !strings.Contains(got, "[c] cancel") {
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
	if got := press(t, m, "k"); !strings.Contains(got, "[G] follow tail") {
		t.Fatalf("footer omits the follow-tail key while the selection is pinned:\n%s", got)
	}

	got := press(t, m, "G")
	if !strings.Contains(got, "seq 1") {
		t.Errorf("the G key did not return the selection to the tail:\n%s", got)
	}
	if strings.Contains(got, "[G] follow tail") {
		t.Errorf("footer still offers follow tail after the pane followed it:\n%s", got)
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

	if got := press(t, m, "tab"); !strings.Contains(got, "[c] cancel") {
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

	if got := m.View().Content; !strings.Contains(got, "[c] cancel") {
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

	// Start the pass, then hold the read open by not draining its command yet.
	_, cmd := m.Update(pollTick(t, m))
	read := make(chan tea.Msg, 1)
	go func() { read <- cmd() }()

	_, quit := m.Update(tea.KeyPressMsg(tea.Key{Text: "q", Code: 'q'}))
	assertQuitCmd(t, quit)

	close(release)
	select {
	case <-read:
	case <-time.After(2 * time.Second):
		t.Fatal("the feed read never returned")
	}
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
	return len(batch) - 1
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
	if !strings.Contains(got, "[q] quit") {
		t.Errorf("focus stayed on the detached pane:\n%s", got)
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

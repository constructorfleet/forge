package tui_test

import (
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
	return tui.NewLiveModel(roster, "ex-1", time.Second), store
}

// nextPollTick drives one poll cycle through the real Init command: it
// returns the model (updated) and the command that schedules the following
// tick.
func nextPollTick(t *testing.T, m *tui.LiveModel) (tea.Model, tea.Cmd) {
	t.Helper()
	cmd := m.Init()
	if cmd == nil {
		t.Fatalf("Init returned nil cmd")
	}
	msg := cmd()
	next, nextCmd := m.Update(msg)
	if nextCmd == nil {
		t.Fatalf("a poll tick must schedule the next tick")
	}
	return next, nextCmd
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

// transcriptModel builds a live model whose transcript pane holds one prose
// event and one tool call, focused on the transcript.
func transcriptModel(t *testing.T) *tui.LiveModel {
	t.Helper()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	nextPollTick(t, m)

	m.SetTranscript(transcriptFixture())
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
// a handler, so the footer never advertises an inert key, and that the key
// clears itself once the pane follows the tail again.
func TestLiveModelFollowTailKeyReachesPane(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	nextPollTick(t, m)

	spy := &fakeScroller{}
	pane := tui.NewTranscriptPane()
	pane.SetScroller(spy)
	pane.SetView(tui.TranscriptViewModel{AtTail: false, Events: []tui.TranscriptEvent{
		{Seq: 0, Type: "MESSAGE", Role: "assistant", Text: "older"},
		{Seq: 1, Type: "MESSAGE", Role: "assistant", Text: "newer"},
	}})
	m.SetTranscript(pane)

	if got := press(t, m, "tab"); !strings.Contains(got, "[G] follow tail") {
		t.Fatalf("footer omits the follow-tail key while scrolled back:\n%s", got)
	}
	got := press(t, m, "G")
	if spy.tails != 1 {
		t.Errorf("ScrollToTail calls = %d, want 1: the G key is inert", spy.tails)
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

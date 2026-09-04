package tui_test

import (
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

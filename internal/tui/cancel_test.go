package tui_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
)

// pressAndRunCmd presses one key, then — like the Bubble Tea runtime — runs
// the command Update returns and delivers its message back through Update,
// which is what fires a key's off-goroutine call and commits its result.
func pressAndRunCmd(t *testing.T, m *tui.LiveModel, name string) string {
	t.Helper()
	key := tea.Key{Text: name, Code: rune(name[0])}
	_, cmd := m.Update(tea.KeyPressMsg(key))
	if cmd != nil {
		if msg := cmd(); msg != nil {
			m.Update(msg)
		}
	}
	return m.View().Content
}

// fakeCanceller is a scripted tui.Canceller double: it records every
// executionID it was asked to cancel, so a test can prove the control fires
// (or does not fire) the in-process action, and it can hold the call open on
// block, so a test can drive the in-flight guard deterministically.
type fakeCanceller struct {
	mu      sync.Mutex
	calls   []string
	err     error
	state   storage.ExecutionState
	block   chan struct{}
	entered chan struct{}
}

func (f *fakeCanceller) CancelExecution(_ context.Context, executionID string) (storage.ExecutionState, error) {
	f.mu.Lock()
	f.calls = append(f.calls, executionID)
	f.mu.Unlock()
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.block != nil {
		<-f.block
	}
	return f.state, f.err
}

func (f *fakeCanceller) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// TestLiveModelCancelKeyArmsConfirmationWithoutCancelling proves the cancel key
// is UI-only confirmation first: the operator sees a confirm prompt and the
// Canceller has not run yet, since the frame-side confirm is not durable state.
func TestLiveModelCancelKeyArmsConfirmationWithoutCancelling(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	nextPollTick(t, m)
	canceller := &fakeCanceller{}
	m.Canceller = canceller

	got := press(t, m, "c")
	if canceller.callCount() != 0 {
		t.Fatalf("cancel key fired the Canceller before confirmation, calls = %v", canceller.calls)
	}
	if !strings.Contains(got, "ex-1") {
		t.Fatalf("frame = %q, want a confirmation naming the execution", got)
	}
}

// TestLiveModelCancelConfirmYFiresCancel proves the second key, y, fires the
// Canceller with the watched executionID, and that the row's own state is
// left untouched: the acknowledgement is pending-until-observed, so only the
// next poll tick's read can show CANCELLED, never an optimistic local edit.
func TestLiveModelCancelConfirmYFiresCancel(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	nextPollTick(t, m)
	canceller := &fakeCanceller{}
	m.Canceller = canceller
	before := m.Workers()[0].State

	press(t, m, "c")
	got := pressAndRunCmd(t, m, "y")

	if canceller.callCount() != 1 {
		t.Fatalf("Canceller called %d times, want 1", canceller.callCount())
	}
	if canceller.calls[0] != "ex-1" {
		t.Fatalf("Canceller called with %q, want the watched execution ex-1", canceller.calls[0])
	}
	if m.Workers()[0].State != before {
		t.Fatalf("row state changed to %v before any poll observed it, want %v (pending-until-observed)", m.Workers()[0].State, before)
	}
	if !strings.Contains(got, "ex-1") {
		t.Fatalf("frame = %q, want an acknowledgement naming the execution", got)
	}
}

// TestLiveModelCancelConfirmDeclineAbandonsCancel proves any key other than y
// abandons the armed confirmation without calling the Canceller.
func TestLiveModelCancelConfirmDeclineAbandonsCancel(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	nextPollTick(t, m)
	canceller := &fakeCanceller{}
	m.Canceller = canceller

	press(t, m, "c")
	got := press(t, m, "n")

	if canceller.callCount() != 0 {
		t.Fatalf("Canceller called %d times, want 0 after a declined confirmation", canceller.callCount())
	}
	if !strings.Contains(got, "declined") {
		t.Fatalf("frame = %q, want a decline notice", got)
	}
}

// TestLiveModelCancelSurfacesFailure proves a failing cancel is reported, not
// silently swallowed.
func TestLiveModelCancelSurfacesFailure(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	nextPollTick(t, m)
	canceller := &fakeCanceller{err: errors.New("store is locked")}
	m.Canceller = canceller

	press(t, m, "c")
	got := pressAndRunCmd(t, m, "y")

	if !strings.Contains(got, "store is locked") {
		t.Fatalf("frame = %q, want the cancel failure surfaced", got)
	}
}

// TestLiveModelCancelSurfacesOwnerWarning proves a CancelOwnerError — the
// cancel completed but a worker owner did not stop or could not be inspected
// — is surfaced too, not treated as silent success.
func TestLiveModelCancelSurfacesOwnerWarning(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	nextPollTick(t, m)
	canceller := &fakeCanceller{err: &engine.CancelOwnerError{Err: errors.New("pid 123 did not stop")}}
	m.Canceller = canceller

	press(t, m, "c")
	got := pressAndRunCmd(t, m, "y")

	if !strings.Contains(got, "pid 123 did not stop") {
		t.Fatalf("frame = %q, want the owner warning surfaced", got)
	}
}

// TestLiveModelCancelInFlightBlocksASecondIssue proves a second cancel key
// press while a call is still running does not double-issue it: the control
// disables only its own in-flight call, per the concurrency rule.
func TestLiveModelCancelInFlightBlocksASecondIssue(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	nextPollTick(t, m)
	canceller := &fakeCanceller{block: make(chan struct{}), entered: make(chan struct{})}
	m.Canceller = canceller

	press(t, m, "c")
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "y", Code: 'y'}))
	if cmd == nil {
		t.Fatal("confirming the cancel returned no command")
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case <-canceller.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the cancel call never started")
	}

	// The first call is now blocked inside the Canceller. A second cancel key
	// press must not start a second call.
	got := press(t, m, "c")
	if canceller.callCount() != 1 {
		t.Fatalf("Canceller called %d times while one was in flight, want 1", canceller.callCount())
	}
	if !strings.Contains(got, "in flight") {
		t.Fatalf("frame = %q, want a notice that a cancel is already in flight", got)
	}

	close(canceller.block)
	select {
	case msg := <-result:
		m.Update(msg)
	case <-time.After(2 * time.Second):
		t.Fatal("the in-flight cancel never returned")
	}
}

// TestLiveModelCancelWithoutACancellerExplains proves a nil Canceller (the
// control not wired up) explains itself instead of silently doing nothing.
func TestLiveModelCancelWithoutACancellerExplains(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	nextPollTick(t, m)

	press(t, m, "c")
	got := press(t, m, "y")

	if !strings.Contains(got, "not available") {
		t.Fatalf("frame = %q, want a notice that cancel is unavailable", got)
	}
}

// TestLiveModelCancelKeyOnTerminalRowDoesNothing proves the cancel key is
// inert on a row whose state is already terminal, mirroring the footer's own
// legality (LegalKeys omits c for a terminal state).
func TestLiveModelCancelKeyOnTerminalRowDoesNothing(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "Done", State: domain.StateDone, StateChangedAt: now},
			},
		},
	}
	m := tui.NewLiveModel(tui.NewRoster(store, func() time.Time { return now }), "ex-1", time.Millisecond)
	nextPollTick(t, m)
	canceller := &fakeCanceller{}
	m.Canceller = canceller

	got := press(t, m, "c")

	if canceller.callCount() != 0 {
		t.Fatalf("cancel key fired the Canceller on a terminal row, calls = %v", canceller.calls)
	}
	if strings.Contains(got, "cancel execution") {
		t.Fatalf("frame = %q, want no confirmation armed on a terminal row", got)
	}
}

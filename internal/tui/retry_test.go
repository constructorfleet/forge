package tui_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
)

// failedFixture builds a live model with one FAILED Worker, so the retry key
// has a legal row to act on without every test repeating the store shape.
func failedFixture(t *testing.T, now time.Time) *tui.LiveModel {
	t.Helper()
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "Write tests", State: domain.StateFailed, StateChangedAt: now},
			},
		},
	}
	m := tui.NewLiveModel(tui.NewRoster(store, func() time.Time { return now }), "ex-1", time.Millisecond)
	nextPollTick(t, m)
	return m
}

// fakeRetrier is a scripted tui.Retrier double: it records every
// (executionID, issueID) pair it was asked to retry, so a test can prove the
// control spawns (or does not spawn) the detached child, and it can hold the
// call open on block, so a test can drive the in-flight guard deterministically.
type fakeRetrier struct {
	mu      sync.Mutex
	calls   [][2]string
	result  tui.RetryResult
	err     error
	block   chan struct{}
	entered chan struct{}
}

func (f *fakeRetrier) Retry(executionID, issueID string) (tui.RetryResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, [2]string{executionID, issueID})
	f.mu.Unlock()
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.block != nil {
		<-f.block
	}
	return f.result, f.err
}

func (f *fakeRetrier) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// TestLiveModelRetryKeyFiresOnFailedRow proves the retry key spawns the
// detached child (via the Retrier seam) for the selected FAILED Worker, with
// no confirmation step: retry, unlike cancel, is not a destructive
// store-mutating call in this process.
func TestLiveModelRetryKeyFiresOnFailedRow(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := failedFixture(t, now)
	retrier := &fakeRetrier{}
	m.Retrier = retrier

	got := pressAndRunCmd(t, m, "r")

	if retrier.callCount() != 1 {
		t.Fatalf("Retrier called %d times, want 1", retrier.callCount())
	}
	if retrier.calls[0] != [2]string{"ex-1", "#1"} {
		t.Fatalf("Retrier called with %v, want [ex-1 #1]", retrier.calls[0])
	}
	if !strings.Contains(got, "#1") {
		t.Fatalf("frame = %q, want an acknowledgement naming the issue", got)
	}
}

// TestLiveModelRetryKeyOnNonFailedRowDoesNothing proves the retry key is
// inert on a row whose state is not FAILED, mirroring the footer's own
// legality (LegalKeys offers r only from FAILED).
func TestLiveModelRetryKeyOnNonFailedRowDoesNothing(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	nextPollTick(t, m)
	retrier := &fakeRetrier{}
	m.Retrier = retrier

	got := press(t, m, "r")

	if retrier.callCount() != 0 {
		t.Fatalf("retry key fired the Retrier on a non-FAILED row, calls = %v", retrier.calls)
	}
	if strings.Contains(got, "retrying issue") {
		t.Fatalf("frame = %q, want no retry started on an illegal row", got)
	}
}

// TestLiveModelRetrySurfacesFailure proves a failing spawn is reported, not
// silently swallowed: the control must surface child failures.
func TestLiveModelRetrySurfacesFailure(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := failedFixture(t, now)
	retrier := &fakeRetrier{err: errors.New("retry child exited 1: base rebase conflict")}
	m.Retrier = retrier

	got := pressAndRunCmd(t, m, "r")

	if !strings.Contains(got, "base rebase conflict") {
		t.Fatalf("frame = %q, want the retry failure surfaced", got)
	}
}

// TestLiveModelRetryInFlightBlocksASecondIssue proves a second retry key
// press while a spawn is still running does not double-issue it.
func TestLiveModelRetryInFlightBlocksASecondIssue(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := failedFixture(t, now)
	retrier := &fakeRetrier{block: make(chan struct{}), entered: make(chan struct{})}
	m.Retrier = retrier

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "r", Code: 'r'}))
	if cmd == nil {
		t.Fatal("the retry key returned no command")
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case <-retrier.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the retry call never started")
	}

	got := press(t, m, "r")
	if retrier.callCount() != 1 {
		t.Fatalf("Retrier called %d times while one was in flight, want 1", retrier.callCount())
	}
	if !strings.Contains(got, "in flight") {
		t.Fatalf("frame = %q, want a notice that a retry is already in flight", got)
	}

	close(retrier.block)
	select {
	case msg := <-result:
		m.Update(msg)
	case <-time.After(2 * time.Second):
		t.Fatal("the in-flight retry never returned")
	}
}

// TestLiveModelRetryWithoutARetrierExplains proves a nil Retrier (the
// control not wired up) explains itself instead of silently doing nothing.
func TestLiveModelRetryWithoutARetrierExplains(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := failedFixture(t, now)

	got := press(t, m, "r")

	if !strings.Contains(got, "not available") {
		t.Fatalf("frame = %q, want a notice that retry is unavailable", got)
	}
}

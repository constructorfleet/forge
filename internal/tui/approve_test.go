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
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
)

// fakeApprover is a scripted tui.Approver double: it records every issueID it
// was asked to resume, so a test can prove the control fires (or does not
// fire) the in-process write, and it can hold the call open on block, so a
// test can drive the in-flight guard deterministically.
type fakeApprover struct {
	mu      sync.Mutex
	calls   []string
	err     error
	block   chan struct{}
	entered chan struct{}
}

func (f *fakeApprover) ResumeAfterReplan(_ context.Context, _, issueID string) (domain.Issue, error) {
	f.mu.Lock()
	f.calls = append(f.calls, issueID)
	f.mu.Unlock()
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.block != nil {
		<-f.block
	}
	return domain.Issue{}, f.err
}

func (f *fakeApprover) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// approveFixture builds a live model over one NEEDS_REPLAN Issue whose replan
// checkpoint is recorded.
func approveFixture(t *testing.T, now time.Time) *tui.LiveModel {
	t.Helper()
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "Add axis labels", State: domain.StateNeedsReplan, StateChangedAt: now.Add(-time.Minute)},
			},
		},
		checkpoints: map[string]storage.ReplanCheckpoint{
			"#1": {
				ExecutionID: "ex-1", IssueID: "#1", FeatureID: "feat-1",
				Reason: "requirement changed underneath the ticket",
			},
		},
	}
	return tui.NewLiveModel(tui.NewRoster(store, func() time.Time { return now }), "ex-1", time.Second)
}

// pressApproveKey presses the approve key and drives the returned command's
// message back through Update, mirroring pressDiffKey: the store read runs
// inside the command, so the pager only opens on that second pass.
func pressApproveKey(t *testing.T, m *tui.LiveModel) tea.Cmd {
	t.Helper()
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "p", Code: 'p'}))
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	_, next := m.Update(msg)
	return next
}

// driveApprove presses the approve key and drives every command the whole
// flow returns — the checkpoint read, the pager open, and (once the pager
// "closes") the ResumeAfterReplan call — the same way the Bubble Tea runtime
// would. It returns the rendered frame after the whole chain settles.
func driveApprove(t *testing.T, m *tui.LiveModel) string {
	t.Helper()
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "p", Code: 'p'}))
	runImmediateCmd(t, m, cmd)
	return m.View().Content
}

// TestLiveModelApproveKeyDefersToThePager proves the approve key hands the
// stored replan checkpoint to the pager seam before ever writing anything.
func TestLiveModelApproveKeyDefersToThePager(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := approveFixture(t, now)
	var opened []string
	m.OpenApprove = func(_, artifact string) tea.Cmd {
		opened = append(opened, artifact)
		return func() tea.Msg { return nil }
	}
	approver := &fakeApprover{}
	m.Approver = approver
	nextPollTick(t, m)

	if cmd := pressApproveKey(t, m); cmd == nil {
		t.Fatal("the approve key produced no pager command, want the pager's own command")
	}
	if len(opened) != 1 {
		t.Fatalf("pager opened %d times, want 1", len(opened))
	}
	if !strings.Contains(opened[0], "requirement changed underneath the ticket") {
		t.Fatalf("pager received %q, want the stored replan checkpoint", opened[0])
	}
	if approver.callCount() != 0 {
		t.Fatalf("Approver called before the pager closed, calls = %v", approver.calls)
	}
}

// TestLiveModelApproveFiresAfterThePagerCloses proves the pager closing (not
// the key press) is what fires ResumeAfterReplan, and that the row's own
// state is left untouched: the acknowledgement is pending-until-observed, so
// only the next poll tick's read can show READY, never an optimistic local
// edit.
func TestLiveModelApproveFiresAfterThePagerCloses(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := approveFixture(t, now)
	m.OpenApprove = func(string, string) tea.Cmd { return func() tea.Msg { return tui.ApproveClosedMsg{} } }
	approver := &fakeApprover{}
	m.Approver = approver
	nextPollTick(t, m)
	before := m.Workers()[0].State

	got := driveApprove(t, m)

	if approver.callCount() != 1 {
		t.Fatalf("Approver called %d times, want 1", approver.callCount())
	}
	if approver.calls[0] != "#1" {
		t.Fatalf("Approver called with %q, want the read Issue #1", approver.calls[0])
	}
	if m.Workers()[0].State != before {
		t.Fatalf("row state changed to %v before any poll observed it, want %v (pending-until-observed)", m.Workers()[0].State, before)
	}
	if !strings.Contains(got, "#1") {
		t.Fatalf("frame = %q, want an acknowledgement naming the issue", got)
	}
}

// TestLiveModelApproveSurfacesFailure proves a failing approve is reported,
// not silently swallowed.
func TestLiveModelApproveSurfacesFailure(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := approveFixture(t, now)
	m.OpenApprove = func(string, string) tea.Cmd { return func() tea.Msg { return tui.ApproveClosedMsg{} } }
	m.Approver = &fakeApprover{err: errors.New("feature feat-1 is frozen pending replan")}
	nextPollTick(t, m)

	got := driveApprove(t, m)

	if !strings.Contains(got, "feature feat-1 is frozen pending replan") {
		t.Fatalf("frame = %q, want the approve failure surfaced", got)
	}
}

// TestLiveModelApproveInFlightBlocksASecondIssue proves a second approve key
// press while a call is still running does not double-issue it.
func TestLiveModelApproveInFlightBlocksASecondIssue(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := approveFixture(t, now)
	m.OpenApprove = func(string, string) tea.Cmd { return func() tea.Msg { return tui.ApproveClosedMsg{} } }
	approver := &fakeApprover{block: make(chan struct{}), entered: make(chan struct{})}
	m.Approver = approver
	nextPollTick(t, m)

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "p", Code: 'p'}))
	if cmd == nil {
		t.Fatal("the approve key's checkpoint read returned no command")
	}
	msg := cmd()
	_, cmd2 := m.Update(msg)
	if cmd2 == nil {
		t.Fatal("the approve key's pager open returned no command")
	}
	pagerMsg := cmd2()
	_, startCmd := m.Update(pagerMsg)
	if startCmd == nil {
		t.Fatal("the pager close returned no command")
	}

	result := make(chan tea.Msg, 1)
	go func() { result <- startCmd() }()
	select {
	case <-approver.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the approve call never started")
	}

	// The first call is now blocked inside the Approver. A second approve key
	// press must not start a second call.
	got := press(t, m, "p")
	if approver.callCount() != 1 {
		t.Fatalf("Approver called %d times while one was in flight, want 1", approver.callCount())
	}
	if !strings.Contains(got, "in flight") {
		t.Fatalf("frame = %q, want a notice that an approve is already in flight", got)
	}

	close(approver.block)
	select {
	case msg := <-result:
		m.Update(msg)
	case <-time.After(2 * time.Second):
		t.Fatal("the in-flight approve never returned")
	}
}

// TestLiveModelApproveWithoutAnApproverExplains proves a nil Approver (the
// control not wired up) explains itself instead of silently doing nothing,
// only once the write is actually attempted.
func TestLiveModelApproveWithoutAnApproverExplains(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := approveFixture(t, now)
	m.OpenApprove = func(string, string) tea.Cmd { return func() tea.Msg { return tui.ApproveClosedMsg{} } }
	nextPollTick(t, m)

	got := driveApprove(t, m)

	if !strings.Contains(got, "not available") {
		t.Fatalf("frame = %q, want a notice that approve is unavailable", got)
	}
}

// TestLiveModelApproveKeyOnIneligibleRowDoesNothing proves the approve key is
// inert on a row not parked in NEEDS_REPLAN, mirroring the footer's own
// legality (LegalKeys offers p only for NEEDS_REPLAN).
func TestLiveModelApproveKeyOnIneligibleRowDoesNothing(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	approver := &fakeApprover{}
	m.Approver = approver
	nextPollTick(t, m)

	got := press(t, m, "p")

	if approver.callCount() != 0 {
		t.Fatalf("approve key fired the Approver on an ineligible row, calls = %v", approver.calls)
	}
	if strings.Contains(got, "opening replan artifact") {
		t.Fatalf("frame = %q, want no pager armed on an ineligible row", got)
	}
}

// TestLiveModelApproveWithNoCheckpointExplains proves a NEEDS_REPLAN row with
// no recorded checkpoint reports that, rather than opening an empty pager.
func TestLiveModelApproveWithNoCheckpointExplains(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "t", State: domain.StateNeedsReplan, StateChangedAt: now},
			},
		},
	}
	m := tui.NewLiveModel(tui.NewRoster(store, func() time.Time { return now }), "ex-1", time.Millisecond)
	called := false
	m.OpenApprove = func(string, string) tea.Cmd { called = true; return nil }
	nextPollTick(t, m)

	if cmd := pressApproveKey(t, m); cmd != nil {
		t.Fatal("the approve key produced a pager command with no stored checkpoint")
	}
	if called {
		t.Fatal("the pager opened with no stored checkpoint")
	}
	if !strings.Contains(m.View().Content, "no replan checkpoint") {
		t.Fatalf("frame = %q, want a notice that no checkpoint exists", m.View().Content)
	}
}

// TestFrameOffersTheApproveKeyOnlyOnNeedsReplan proves the footer never
// advertises approve outside NEEDS_REPLAN.
func TestFrameOffersTheApproveKeyOnlyOnNeedsReplan(t *testing.T) {
	with := tui.Render(tui.ViewModel{Workers: []tui.WorkerRow{
		{IssueID: "#1", Title: "t", State: domain.StateNeedsReplan},
	}})
	if !strings.Contains(with, "[p] approve") {
		t.Fatalf("frame = %q, want the approve key", with)
	}

	without := tui.Render(tui.ViewModel{Workers: []tui.WorkerRow{
		{IssueID: "#1", Title: "t", State: domain.StateImplementing},
	}})
	if strings.Contains(without, "[p] approve") {
		t.Fatalf("frame = %q, want no approve key outside NEEDS_REPLAN", without)
	}
}

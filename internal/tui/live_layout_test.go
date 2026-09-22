package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
)

// reviewModel builds a live model over one REVIEWING Issue with an
// implementation run and a "bugs" review run whose events interleave, a stored
// diff, and a poll already applied, so the tests can drive the agents pane, the
// output filter, and the diff pane.
func reviewModel(t *testing.T) (*tui.LiveModel, *fakeRosterStore) {
	t.Helper()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{{
				ID: "#1", Title: "Update the TUI", State: domain.StateReviewing,
				StateChangedAt: now.Add(-30 * time.Second),
			}},
		},
		claimOK: map[string]bool{"#1": true},
		claims:  map[string]storage.WorkerClaim{"#1": {LastHeartbeat: now.Add(-3 * time.Second)}},
		agents: map[string][]storage.TranscriptAgent{"#1": {
			{AgentRunID: 1, Phase: "IMPLEMENTING", Events: 2, Last: storage.TranscriptEvent{Type: "MESSAGE", Text: "implemented", OccurredAt: now.Add(-20 * time.Second)}},
			{AgentRunID: 2, Phase: "REVIEWING", Subagent: "bugs", Events: 2, Last: storage.TranscriptEvent{Type: "MESSAGE", Text: "no bugs", OccurredAt: now.Add(-time.Second)}},
		}},
		reviews: map[string][]storage.ReviewRun{
			"#1": {{Verdict: "APPROVED", Diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1,2 @@\n+added line\n+second\n-gone\n"}},
		},
	}
	m := tui.NewLiveModel(tui.NewRoster(store, func() time.Time { return now }), "ex-1", time.Millisecond)
	m.SetFeed(tui.NewTranscriptFeed(agentFilterFixture()))
	nextPollTick(t, m)
	return m, store
}

// TestLiveModelTabCyclesEveryPane proves tab walks the execution list, the
// agents pane, the output pane, and (once open) the diff pane, then returns to
// the list, with the footer following the focus each step.
func TestLiveModelTabCyclesEveryPane(t *testing.T) {
	m, _ := reviewModel(t)
	if got := visible(m.View().Content); !strings.Contains(got, "[c] cancel") {
		t.Fatalf("initial footer is not the roster's:\n%s", got)
	}
	if got := visible(press(t, m, "tab")); !strings.Contains(got, "[j/k] switch agent") {
		t.Fatalf("first tab did not focus the agents pane:\n%s", got)
	}
	if got := visible(press(t, m, "tab")); !strings.Contains(got, "[enter] expand") || strings.Contains(got, "[c] cancel") {
		t.Fatalf("second tab did not focus the output pane:\n%s", got)
	}
	if got := visible(press(t, m, "tab")); !strings.Contains(got, "[c] cancel") {
		t.Fatalf("third tab (diff closed) did not return to the roster:\n%s", got)
	}
	pressDiffKey(t, m)
	if got := visible(m.View().Content); !strings.Contains(got, "[enter] open in $PAGER") {
		t.Fatalf("opening the diff did not focus the diff pane:\n%s", got)
	}
	if got := visible(press(t, m, "tab")); !strings.Contains(got, "[c] cancel") {
		t.Fatalf("tab from the diff pane did not return to the roster:\n%s", got)
	}
}

// TestLiveModelAgentKeysFilterTheOutput proves the output pane shows the
// implementation Agent's events by default, and j/k in the agents pane switch
// it to the review subagent's own events.
func TestLiveModelAgentKeysFilterTheOutput(t *testing.T) {
	m, _ := reviewModel(t)
	got := m.View().Content
	if !strings.Contains(got, "implemented") || strings.Contains(got, "reviewing bugs") {
		t.Fatalf("default output is not the implementation Agent's alone:\n%s", got)
	}
	if !strings.Contains(got, "> implementation") || !strings.Contains(got, "  review: bugs") {
		t.Fatalf("agents pane does not mark the implementation row:\n%s", got)
	}

	press(t, m, "tab")
	got = press(t, m, "j")
	if !strings.Contains(got, "> review: bugs") {
		t.Fatalf("j did not move the agent selection:\n%s", got)
	}
	if !strings.Contains(got, "reviewing bugs") || strings.Contains(got, "implemented") || strings.Contains(got, "go vet") {
		t.Fatalf("output pane did not switch to the review subagent:\n%s", got)
	}
	if !strings.Contains(got, "review: bugs | events 2 | last no bugs") {
		t.Fatalf("strip does not describe the selected agent:\n%s", got)
	}

	// The filter survives a poll.
	nextPollTick(t, m)
	if got := m.View().Content; strings.Contains(got, "implemented") {
		t.Fatalf("a poll reset the agent filter:\n%s", got)
	}

	got = press(t, m, "k")
	if !strings.Contains(got, "implemented") || strings.Contains(got, "reviewing bugs") {
		t.Fatalf("k did not return the output to the implementation Agent:\n%s", got)
	}
	if got := press(t, m, "k"); !strings.Contains(got, "> implementation") {
		t.Fatalf("k did not clamp at the first agent:\n%s", got)
	}
}

// TestLiveModelDiffKeyTogglesThePane proves d opens the diff pane, its
// summary loads off the update goroutine, and a second d closes it.
func TestLiveModelDiffKeyTogglesThePane(t *testing.T) {
	m, store := reviewModel(t)
	if strings.Contains(m.View().Content, "a.go") {
		t.Fatal("diff pane is open before the key")
	}
	if cmd := pressDiffKey(t, m); cmd != nil {
		t.Fatalf("opening the diff pane returned a follow-up command, want none")
	}
	got := visible(m.View().Content)
	for _, want := range []string{"DIFF (1 files)", "a.go", "+added line", "[d] hide diff"} {
		if !strings.Contains(got, want) {
			t.Errorf("open diff pane omits %q:\n%s", want, got)
		}
	}
	if store.runReads != 1 {
		t.Errorf("opening the pane read the diff %d times, want 1", store.runReads)
	}
	if got := visible(press(t, m, "d")); strings.Contains(got, "a.go") || !strings.Contains(got, "[d] diff") {
		t.Fatalf("second d did not close the diff pane:\n%s", got)
	}
}

// TestLiveModelDiffPaneEnterDefersToThePager proves enter in the diff pane
// hands the stored diff to $PAGER, so the body still never enters the frame.
func TestLiveModelDiffPaneEnterDefersToThePager(t *testing.T) {
	m, _ := reviewModel(t)
	var opened []string
	m.OpenDiff = func(_, diff string) tea.Cmd {
		opened = append(opened, diff)
		return func() tea.Msg { return nil }
	}
	pressDiffKey(t, m)
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: '\r'}))
	if cmd == nil {
		t.Fatal("enter in the diff pane produced no command")
	}
	if msg := cmd(); msg != nil {
		m.Update(msg)
	}
	if len(opened) != 1 || !strings.Contains(opened[0], "+added line") {
		t.Fatalf("pager opened with %v, want the stored diff once", opened)
	}
	if !strings.Contains(m.View().Content, "DIFF") {
		t.Fatalf("the diff pane disappeared after pager return:\n%s", m.View().Content)
	}
}

// TestLiveModelDiffPaneReloadsWhenTheReviewChanges proves an open pane follows
// a new Review verdict with a fresh read, and does not re-read on every poll.
func TestLiveModelDiffPaneReloadsWhenTheReviewChanges(t *testing.T) {
	m, store := reviewModel(t)
	pressDiffKey(t, m)
	nextPollTick(t, m)
	nextPollTick(t, m)
	if store.runReads != 1 {
		t.Fatalf("polls with an unchanged review read the diff %d times, want 1", store.runReads)
	}
	store.reviews["#1"] = append(store.reviews["#1"], storage.ReviewRun{Verdict: "CHANGES_REQUIRED", Diff: "--- a/b.go\n+++ b/b.go\n+one\n"})
	nextPollTick(t, m)
	if store.runReads != 2 {
		t.Fatalf("a changed review did not reload the diff (reads = %d)", store.runReads)
	}
	if got := m.View().Content; !strings.Contains(got, "b.go") {
		t.Fatalf("diff pane did not show the new review's file:\n%s", got)
	}
}

// TestLiveModelEnterOnRosterInspectsTheOutput proves enter on the execution
// list moves focus to the output pane, the "inspect" the footer advertises.
func TestLiveModelEnterOnRosterInspectsTheOutput(t *testing.T) {
	m, _ := reviewModel(t)
	got := visible(press(t, m, "enter"))
	if !strings.Contains(got, "[enter] expand") || strings.Contains(got, "[c] cancel") {
		t.Fatalf("enter did not focus the output pane:\n%s", got)
	}
}

// TestLiveModelWorkerSwitchResetsTheAgentSelection proves moving to another
// Worker lands on its implementation row rather than an agent index it may
// not have.
func TestLiveModelWorkerSwitchResetsTheAgentSelection(t *testing.T) {
	m, store := reviewModel(t)
	store.state.Issues = append(store.state.Issues, domain.Issue{ID: "#2", Title: "Second", State: domain.StatePending})
	nextPollTick(t, m)
	press(t, m, "tab")
	press(t, m, "j")
	press(t, m, "tab")
	press(t, m, "tab")
	got := press(t, m, "j")
	if !strings.Contains(got, "> implementation") {
		t.Fatalf("switching Worker kept a stale agent selection:\n%s", got)
	}
}

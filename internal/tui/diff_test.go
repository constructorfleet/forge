package tui_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
)

// staticEnv builds an environment lookup over a fixed map.
func staticEnv(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

// TestPagerCommandUsesPagerEnvironment proves $PAGER decides the command, with
// its own arguments honoured, and that the artifact path comes last.
func TestPagerCommandUsesPagerEnvironment(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"pager with arguments", map[string]string{"PAGER": "less -R"}, []string{"less", "-R", "/tmp/d.diff"}},
		{"bare pager", map[string]string{"PAGER": "bat"}, []string{"bat", "/tmp/d.diff"}},
		{"unset falls back", nil, []string{"less", "-R", "/tmp/d.diff"}},
		{"blank falls back", map[string]string{"PAGER": "   "}, []string{"less", "-R", "/tmp/d.diff"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tui.PagerCommand(staticEnv(tc.env), "/tmp/d.diff")
			if len(got) != len(tc.want) {
				t.Fatalf("PagerCommand = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("PagerCommand = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestLatestDiffReadsTheLastReviewRun proves the diff comes from the store's
// only copy (review_runs.diff), taking the last recorded run.
func TestLatestDiffReadsTheLastReviewRun(t *testing.T) {
	store := &fakeRosterStore{reviews: map[string][]storage.ReviewRun{
		"#1": {{Verdict: "CHANGES_REQUIRED", Diff: "old diff"}, {Verdict: "APPROVED", Diff: "new diff"}},
	}}

	got, err := tui.LatestDiff(context.Background(), store, "ex-1", "#1")
	if err != nil {
		t.Fatalf("LatestDiff: %v", err)
	}
	if got != "new diff" {
		t.Fatalf("LatestDiff = %q, want the last run's diff", got)
	}
}

// TestLatestDiffWithoutADiffReportsNoDiff proves an Issue with no stored diff
// reports ErrNoDiff, so the caller can refuse instead of paging emptiness.
func TestLatestDiffWithoutADiffReportsNoDiff(t *testing.T) {
	store := &fakeRosterStore{reviews: map[string][]storage.ReviewRun{
		"#1": {{Verdict: "APPROVED", Diff: ""}},
	}}

	if _, err := tui.LatestDiff(context.Background(), store, "ex-1", "#1"); !errors.Is(err, tui.ErrNoDiff) {
		t.Fatalf("LatestDiff error = %v, want ErrNoDiff", err)
	}
	empty := &fakeRosterStore{}
	if _, err := tui.LatestDiff(context.Background(), empty, "ex-1", "#1"); !errors.Is(err, tui.ErrNoDiff) {
		t.Fatalf("LatestDiff error = %v, want ErrNoDiff for an unreviewed Issue", err)
	}
}

// TestWriteDiffArtifactHandsThePagerAFile proves the diff reaches the pager as
// a file argument, not on stdin: the pager keeps the terminal for its own keys.
func TestWriteDiffArtifactHandsThePagerAFile(t *testing.T) {
	dir := t.TempDir()

	path, err := tui.WriteDiffArtifact(dir, "diff --git a/a.go b/a.go\n")
	if err != nil {
		t.Fatalf("WriteDiffArtifact: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("path = %q, want a file inside %q", path, dir)
	}
	body, err := os.ReadFile(path) //nolint:gosec // the path is this test's own temp file.
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if string(body) != "diff --git a/a.go b/a.go\n" {
		t.Fatalf("artifact = %q, want the diff verbatim", body)
	}
}

// diffFixture builds a live model over one REVIEWING Issue whose last Review
// stored a diff.
func diffFixture(t *testing.T, now time.Time) *tui.LiveModel {
	t.Helper()
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "Add axis labels", State: domain.StateReviewing, StateChangedAt: now.Add(-time.Minute)},
			},
		},
		reviews: map[string][]storage.ReviewRun{
			"#1": {{Verdict: "CHANGES_REQUIRED", Diff: "diff --git a/a.go b/a.go\n+added line\n"}},
		},
	}
	return tui.NewLiveModel(tui.NewRoster(store, func() time.Time { return now }), "ex-1", time.Second)
}

// pressDiffKey presses the diff key and drives the returned command's message
// back through Update, which is what the Bubble Tea runtime does. The store read
// runs inside the command, so the pager only opens on that second pass.
func pressDiffKey(t *testing.T, m *tui.LiveModel) tea.Cmd {
	t.Helper()
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "d", Code: 'd'}))
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

// TestLiveModelDiffKeyDefersToThePager proves the diff key hands the stored
// diff to the pager seam, so no diff ever enters the frame.
func TestLiveModelDiffKeyDefersToThePager(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := diffFixture(t, now)
	var opened []string
	m.OpenDiff = func(_, diff string) tea.Cmd {
		opened = append(opened, diff)
		return func() tea.Msg { return nil }
	}
	nextPollTick(t, m)

	if cmd := pressDiffKey(t, m); cmd == nil {
		t.Fatal("the diff key produced no pager command, want the pager's own command")
	}
	if len(opened) != 1 {
		t.Fatalf("pager opened %d times, want 1", len(opened))
	}
	if !strings.Contains(opened[0], "+added line") {
		t.Fatalf("pager received %q, want the stored diff", opened[0])
	}
}

// TestLiveModelDiffKeyWithoutADiffExplains proves the key is inert with no
// stored diff, and says so instead of opening an empty pager.
func TestLiveModelDiffKeyWithoutADiffExplains(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	called := false
	m.OpenDiff = func(string, string) tea.Cmd {
		called = true
		return nil
	}
	nextPollTick(t, m)

	if cmd := pressDiffKey(t, m); cmd != nil {
		t.Fatal("the diff key produced a pager command with no stored diff")
	}
	if called {
		t.Fatal("the pager opened with no stored diff")
	}
	if !strings.Contains(m.View().Content, "no diff") {
		t.Fatalf("frame = %q, want a notice that no diff exists", m.View().Content)
	}
}

// TestFrameOffersTheDiffKeyOnlyWithADiff proves the footer never advertises a
// pager the store cannot serve.
func TestFrameOffersTheDiffKeyOnlyWithADiff(t *testing.T) {
	with := tui.Render(tui.ViewModel{Workers: []tui.WorkerRow{
		{IssueID: "#1", Title: "t", State: domain.StateReviewing, Verdict: "APPROVED", HasDiff: true},
	}})
	if !strings.Contains(with, "[d] diff") {
		t.Fatalf("frame = %q, want the diff key", with)
	}

	without := tui.Render(tui.ViewModel{Workers: []tui.WorkerRow{
		{IssueID: "#1", Title: "t", State: domain.StateReviewing},
	}})
	if strings.Contains(without, "[d] diff") {
		t.Fatalf("frame = %q, want no diff key without a stored diff", without)
	}
}

// TestFrameRendersNoInlineDiff proves the frame carries no diff content: the
// diff is a heavy artifact and defers out, so nothing lexes or paginates it
// inline. The view-model has no field that could carry one.
func TestFrameRendersNoInlineDiff(t *testing.T) {
	frame := tui.Render(tui.ViewModel{Workers: []tui.WorkerRow{
		{IssueID: "#1", Title: "t", State: domain.StateReviewing, Verdict: "CHANGES_REQUIRED", HasDiff: true},
	}})
	for _, marker := range []string{"diff --git", "@@", "+++", "---"} {
		if strings.Contains(frame, marker) {
			t.Fatalf("frame = %q, want no inline diff (found %q)", frame, marker)
		}
	}
}

// TestLiveModelActionNoticeSurvivesAPollTick proves an operator-facing notice
// outlives the next poll: a key that declines must explain itself for longer
// than one poll interval.
func TestLiveModelActionNoticeSurvivesAPollTick(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	m.OpenDiff = func(string, string) tea.Cmd { return nil }
	nextPollTick(t, m)

	pressDiffKey(t, m)
	if !strings.Contains(m.View().Content, "no diff") {
		t.Fatalf("frame = %q, want the action notice", m.View().Content)
	}

	nextPollTick(t, m)
	if !strings.Contains(m.View().Content, "no diff") {
		t.Fatalf("frame = %q, want the action notice to survive a poll tick", m.View().Content)
	}

	m.Update(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
	if strings.Contains(m.View().Content, "no diff") {
		t.Fatalf("frame = %q, want the next key press to clear the action notice", m.View().Content)
	}
}

// TestLiveModelQuitRemovesTheDiffArtifacts proves the temp diff files do not
// outlive the session: a pager killed before its callback runs leaves a file,
// so quit removes the whole session directory.
func TestLiveModelQuitRemovesTheDiffArtifacts(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := diffFixture(t, now)
	var dir string
	m.OpenDiff = func(d, diff string) tea.Cmd {
		dir = d
		if _, err := tui.WriteDiffArtifact(d, diff); err != nil {
			t.Fatalf("WriteDiffArtifact: %v", err)
		}
		return func() tea.Msg { return nil }
	}
	nextPollTick(t, m)
	pressDiffKey(t, m)
	if dir == "" {
		t.Fatal("the pager seam received no artifact directory")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("stat artifact dir: %v", err)
	}

	m.Update(tea.KeyPressMsg(tea.Key{Text: "q", Code: 'q'}))
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("artifact dir survived quit (err = %v)", err)
	}
}

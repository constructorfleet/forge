package ci_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/ci"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// stubTrackerWithStaleness adds tracker.MergeStatusGetter to stubTracker,
// reporting Behind on the first call and Behind: false thereafter — the
// shape a real Tracker takes once Wait's rebase+force-push has moved the
// pull request off its stale base (issue 233).
type stubTrackerWithStaleness struct {
	stubTracker
	behindCalls  int
	behindUntil  int
	mergedAfter  int
	mergeStatErr error
}

func (s *stubTrackerWithStaleness) GetPullRequestMergeStatus(context.Context, int) (tracker.PullRequestMergeStatus, error) {
	s.behindCalls++
	if s.mergeStatErr != nil {
		return tracker.PullRequestMergeStatus{}, s.mergeStatErr
	}
	return tracker.PullRequestMergeStatus{
		Merged: s.mergedAfter > 0 && s.behindCalls >= s.mergedAfter,
		Behind: s.behindCalls <= s.behindUntil,
	}, nil
}

// stubRebaser is a minimal ci.Rebaser double.
type stubRebaser struct {
	calls     int
	newBases  []string
	conflicts []string
	err       error
}

func (s *stubRebaser) Rebase(_ context.Context, _, _, newBase string) ([]string, error) {
	s.calls++
	s.newBases = append(s.newBases, newBase)
	return s.conflicts, s.err
}

// stubBranchPusher is a minimal ci.BranchPusher double.
type stubBranchPusher struct {
	calls     int
	paths     []string
	branch    []string
	pushErr   error
	resetPath []string
	resetSHA  []string
	resetErr  error
}

func (s *stubBranchPusher) ForcePush(_ context.Context, path, branch string) error {
	s.calls++
	s.paths = append(s.paths, path)
	s.branch = append(s.branch, branch)
	return s.pushErr
}

func (s *stubBranchPusher) Reset(_ context.Context, path, commitSHA string) error {
	s.resetPath = append(s.resetPath, path)
	s.resetSHA = append(s.resetSHA, commitSHA)
	return s.resetErr
}

func seedWorkspace(t *testing.T, store *storage.SQLiteStore, executionID, issueID, path, branch string) {
	t.Helper()
	if err := store.RecordWorkspace(context.Background(), executionID, domain.Workspace{
		IssueID: issueID,
		Path:    path,
		Branch:  branch,
	}); err != nil {
		t.Fatalf("RecordWorkspace: %v", err)
	}
}

func TestWait_StalePR_RebasesAndForcePushesThenContinuesToChecks(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-stale", "40")
	seedWorkspace(t, store, "exec-stale", "40", "/tmp/ws-40", "forge/exec-stale/40")

	trk := &stubTrackerWithStaleness{
		stubTracker: stubTracker{
			mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}},
			checkResponses: [][]tracker.PullRequestCheck{
				{{Name: "build", State: tracker.CheckSuccess}},
			},
		},
		// Wait fetches merge status once per iteration and shares it across
		// pollConflict/pollStale/evaluateMergeEligibility (see supervisor.go's
		// Wait), so the first poll's single call must report Behind: true to
		// drive the rebase, and the second poll's call reports both Behind:
		// false (rebase already applied) and Merged: true (checks pass on
		// the second poll too, so this is also the poll that must observe
		// the PR as merged to transition to DONE).
		behindUntil: 1,
		mergedAfter: 2,
	}

	supervisor := ci.New(store, trk, config.Default(), "main")
	sleep := &sleepRecorder{}
	supervisor.Sleep = sleep.Sleep
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 28, 12, 10, 0, 0, time.UTC) }
	rebaser := &stubRebaser{}
	pusher := &stubBranchPusher{}
	supervisor.Rebaser = rebaser
	supervisor.Pusher = pusher

	state, err := supervisor.Wait(context.Background(), "exec-stale", "40")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE", state)
	}

	if rebaser.calls != 1 {
		t.Fatalf("Rebase calls = %d, want 1", rebaser.calls)
	}
	if rebaser.newBases[0] != "main" {
		t.Fatalf("Rebase newBase = %q, want main", rebaser.newBases[0])
	}
	if pusher.calls != 1 {
		t.Fatalf("ForcePush calls = %d, want 1", pusher.calls)
	}
	if pusher.paths[0] != "/tmp/ws-40" || pusher.branch[0] != "forge/exec-stale/40" {
		t.Fatalf("ForcePush path/branch = %q/%q, want /tmp/ws-40/forge/exec-stale/40", pusher.paths[0], pusher.branch[0])
	}

	runs, err := store.CIRunsByIssue(context.Background(), "exec-stale", "40")
	if err != nil {
		t.Fatalf("CIRunsByIssue: %v", err)
	}
	var staleRuns int
	for _, run := range runs {
		if run.Kind == storage.CIRunKindStale {
			staleRuns++
			if run.Status != storage.CIRunStatusPassed {
				t.Fatalf("stale run status = %s, want PASSED", run.Status)
			}
		}
	}
	if staleRuns != 1 {
		t.Fatalf("stale runs = %d, want 1", staleRuns)
	}
}

func TestWait_StalePR_RebaseConflict_RoutesToNeedsInfoWithoutPushing(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-stale-conflict", "41")
	seedWorkspace(t, store, "exec-stale-conflict", "41", "/tmp/ws-41", "forge/exec-stale-conflict/41")

	trk := &stubTrackerWithStaleness{
		stubTracker: stubTracker{mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}}},
		behindUntil: 100,
	}

	cfg := config.Default()
	cfg.Blocked.Label = "forge-blocked"

	supervisor := ci.New(store, trk, cfg, "main")
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 28, 12, 11, 0, 0, time.UTC) }
	rebaser := &stubRebaser{conflicts: []string{"main.go"}}
	pusher := &stubBranchPusher{}
	supervisor.Rebaser = rebaser
	supervisor.Pusher = pusher
	needsInfo := newStubNeedsInfoTracker()
	supervisor.NeedsInfoTracker = needsInfo

	state, err := supervisor.Wait(context.Background(), "exec-stale-conflict", "41")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateNeedsInfo {
		t.Fatalf("state = %s, want NEEDS_INFO", state)
	}
	if pusher.calls != 0 {
		t.Fatalf("ForcePush calls = %d, want 0 (conflicted rebase must not push)", pusher.calls)
	}
	if trk.checkCalls != 0 {
		t.Fatalf("GetPullRequestChecks calls = %d, want 0 (stale conflict short-circuits checks)", trk.checkCalls)
	}

	runs, err := store.CIRunsByIssue(context.Background(), "exec-stale-conflict", "41")
	if err != nil {
		t.Fatalf("CIRunsByIssue: %v", err)
	}
	if len(runs) != 1 || runs[0].Kind != storage.CIRunKindStale || runs[0].Status != storage.CIRunStatusFailed {
		t.Fatalf("runs = %+v, want one FAILED stale run", runs)
	}
}

func TestWait_StalePR_NoRebaserConfigured_FallsThroughToChecks(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-stale-noop", "42")

	trk := &stubTrackerWithStaleness{
		stubTracker: stubTracker{
			mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}},
			checkResponses: [][]tracker.PullRequestCheck{
				{{Name: "build", State: tracker.CheckSuccess}},
			},
		},
		behindUntil: 100,
		mergedAfter: 2,
	}

	supervisor := ci.New(store, trk, config.Default(), "main")
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 28, 12, 12, 0, 0, time.UTC) }

	state, err := supervisor.Wait(context.Background(), "exec-stale-noop", "42")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE (no Rebaser configured leaves staleness a no-op)", state)
	}

	runs, err := store.CIRunsByIssue(context.Background(), "exec-stale-noop", "42")
	if err != nil {
		t.Fatalf("CIRunsByIssue: %v", err)
	}
	for _, run := range runs {
		if run.Kind == storage.CIRunKindStale {
			t.Fatalf("unexpected stale run recorded with no Rebaser configured: %+v", run)
		}
	}
}

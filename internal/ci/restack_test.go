package ci_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/ci"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// stubOntoRebaser is a minimal double implementing both ci.Rebaser (so it
// can be assigned to Supervisor.Rebaser) and ci.OntoRebaser (the
// restack-specific capability Wait type-asserts for after observing a
// prerequisite merge, ticket 332). It never expects plain Rebase calls in
// these tests — only pollStale exercises that path (see staleness_test.go).
type stubOntoRebaser struct {
	ontoCalls   int
	executionID []string
	issueID     []string
	newBase     []string
	oldBase     []string
	conflicts   []string
	// conflictsByIssue reports conflicts for one dependent only, so a test
	// can make one dependent in a batch conflict and leave the others clean.
	conflictsByIssue map[string][]string
	err              error
}

func (s *stubOntoRebaser) Rebase(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}

func (s *stubOntoRebaser) RebaseOnto(_ context.Context, executionID, issueID, newBase, oldBase string) ([]string, error) {
	s.ontoCalls++
	s.executionID = append(s.executionID, executionID)
	s.issueID = append(s.issueID, issueID)
	s.newBase = append(s.newBase, newBase)
	s.oldBase = append(s.oldBase, oldBase)
	if s.conflictsByIssue != nil {
		return s.conflictsByIssue[issueID], s.err
	}
	return s.conflicts, s.err
}

// stubBranchResetter is a minimal ci.BranchResetter double. It records the
// workspace path and the commit each Reset call asks for, so a test can
// prove restackDependents restores the live workspace to the dependent's
// last published pull-request commit after a force-push failure.
type stubBranchResetter struct {
	paths      []string
	commitSHAs []string
	err        error
}

func (s *stubBranchResetter) Reset(_ context.Context, path, commitSHA string) error {
	s.paths = append(s.paths, path)
	s.commitSHAs = append(s.commitSHAs, commitSHA)
	return s.err
}

// seedDependentPullRequest records a pull request for a stacked dependent.
// Its CommitSHA is the last commit the dependent published, which is the
// commit restackDependents restores the workspace to if the force-push of a
// restacked branch fails.
func seedDependentPullRequest(t *testing.T, store *storage.SQLiteStore, executionID, issueID string, number int, commitSHA string) {
	t.Helper()
	if err := store.RecordPullRequest(context.Background(), storage.PullRequest{
		ExecutionID: executionID,
		IssueID:     issueID,
		Number:      number,
		URL:         "https://example.invalid/pr/" + issueID,
		CommitSHA:   commitSHA,
		CreatedAt:   time.Date(2026, 8, 28, 12, 2, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("RecordPullRequest: %v", err)
	}
}

// exhaustCIBudget persists a CI-failure count at the ceiling seedDependentIssue
// configures, so a test can observe how a restack failure behaves when the
// dependent has no retry budget left.
func exhaustCIBudget(t *testing.T, store *storage.SQLiteStore, executionID, issueID string) {
	t.Helper()
	limits := domain.RetryLimits{Gate: 1, Review: 1, CI: 3}
	budget := domain.NewRetryBudgetFrom(limits, 0, 0, limits.CI)
	if err := store.UpdateRetryBudget(context.Background(), executionID, issueID, budget); err != nil {
		t.Fatalf("UpdateRetryBudget: %v", err)
	}
}

// seedDependentIssue creates an Issue with Dependencies within an
// already-created Execution (see seedIssueWithPR), records a Workspace for
// it when ws is non-nil, and appends a worker.base_captured Event carrying
// oldBase when oldBase is non-empty — the same datum ADR 0018/ticket 330
// pins at a stacked dependent's READY transition, which restackDependents
// must read back as the rebase boundary.
func seedDependentIssue(
	t *testing.T,
	store *storage.SQLiteStore,
	executionID, issueID string,
	deps []domain.Dependency,
	state domain.IssueState,
	oldBase string,
	ws *domain.Workspace,
) {
	t.Helper()
	ctx := context.Background()
	issue := domain.Issue{
		ID:           issueID,
		ExecutionID:  executionID,
		Title:        "Issue " + issueID,
		State:        state,
		Scope:        domain.ScopeManaged,
		Dependencies: deps,
		RetryBudget:  domain.NewRetryBudget(domain.RetryLimits{Gate: 1, Review: 1, CI: 3}),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if ws != nil {
		ws.IssueID = issueID
		if err := store.RecordWorkspace(ctx, executionID, *ws); err != nil {
			t.Fatalf("RecordWorkspace: %v", err)
		}
	}
	if oldBase != "" {
		payload, err := json.Marshal(map[string]string{"base": oldBase})
		if err != nil {
			t.Fatalf("marshal worker.base_captured payload: %v", err)
		}
		if err := store.AppendEvent(ctx, storage.Event{
			ExecutionID: executionID,
			IssueID:     issueID,
			Type:        "worker.base_captured",
			Data:        string(payload),
			OccurredAt:  time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("AppendEvent worker.base_captured: %v", err)
		}
	}
}

// mergedSupervisor builds a Supervisor whose Wait call for the prerequisite
// issue observes required checks green and the pull request merged on the
// very first poll, so the test can focus on the restack side effect.
func mergedSupervisor(store *storage.SQLiteStore, rebaser *stubOntoRebaser, pusher *stubBranchPusher) *ci.Supervisor {
	trk := &stubTrackerWithMergeSequence{
		stubTracker: stubTracker{
			mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}},
			checkResponses: [][]tracker.PullRequestCheck{
				{{Name: "build", State: tracker.CheckSuccess}},
			},
		},
		statuses: []tracker.PullRequestMergeStatus{{Merged: true}},
	}
	supervisor := ci.New(store, trk, config.Default(), "main")
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 28, 12, 30, 0, 0, time.UTC) }
	supervisor.Rebaser = rebaser
	supervisor.Pusher = pusher
	return supervisor
}

func TestWait_PrerequisiteMerges_RestacksInFlightSingleParentDependent(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-restack", "40")
	seedDependentIssue(t, store, "exec-restack", "41",
		[]domain.Dependency{{IssueID: "41", DependsOnID: "40"}},
		domain.StateImplementing,
		"sha-old-41",
		&domain.Workspace{Path: "/tmp/ws-41", Branch: "forge/exec-restack/41"},
	)

	rebaser := &stubOntoRebaser{}
	pusher := &stubBranchPusher{}
	supervisor := mergedSupervisor(store, rebaser, pusher)

	state, err := supervisor.Wait(context.Background(), "exec-restack", "40")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE", state)
	}

	if rebaser.ontoCalls != 1 {
		t.Fatalf("RebaseOnto calls = %d, want 1", rebaser.ontoCalls)
	}
	if rebaser.issueID[0] != "41" {
		t.Fatalf("RebaseOnto issueID = %q, want 41", rebaser.issueID[0])
	}
	if rebaser.newBase[0] != "main" {
		t.Fatalf("RebaseOnto newBase = %q, want main", rebaser.newBase[0])
	}
	if rebaser.oldBase[0] != "sha-old-41" {
		t.Fatalf("RebaseOnto oldBase = %q, want sha-old-41", rebaser.oldBase[0])
	}

	if pusher.calls != 1 {
		t.Fatalf("ForcePush calls = %d, want 1", pusher.calls)
	}
	if pusher.paths[0] != "/tmp/ws-41" || pusher.branch[0] != "forge/exec-restack/41" {
		t.Fatalf("ForcePush path/branch = %q/%q, want /tmp/ws-41 forge/exec-restack/41", pusher.paths[0], pusher.branch[0])
	}
}

func TestWait_PrerequisiteMerges_SkipsMultiParentDependent(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-restack-multi", "40")
	seedDependentIssue(t, store, "exec-restack-multi", "42",
		[]domain.Dependency{
			{IssueID: "42", DependsOnID: "40"},
			{IssueID: "42", DependsOnID: "99"},
		},
		domain.StateImplementing,
		"sha-old-42",
		&domain.Workspace{Path: "/tmp/ws-42", Branch: "forge/exec-restack-multi/42"},
	)

	rebaser := &stubOntoRebaser{}
	pusher := &stubBranchPusher{}
	supervisor := mergedSupervisor(store, rebaser, pusher)

	state, err := supervisor.Wait(context.Background(), "exec-restack-multi", "40")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE", state)
	}
	if rebaser.ontoCalls != 0 {
		t.Fatalf("RebaseOnto calls = %d, want 0 (multi-parent dependent keeps integration-branch behavior)", rebaser.ontoCalls)
	}
	if pusher.calls != 0 {
		t.Fatalf("ForcePush calls = %d, want 0", pusher.calls)
	}
}

func TestWait_PrerequisiteMerges_SkipsNotYetStartedDependent(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-restack-notstarted", "40")
	seedDependentIssue(t, store, "exec-restack-notstarted", "43",
		[]domain.Dependency{{IssueID: "43", DependsOnID: "40"}},
		domain.StateReady,
		"",
		nil,
	)

	rebaser := &stubOntoRebaser{}
	pusher := &stubBranchPusher{}
	supervisor := mergedSupervisor(store, rebaser, pusher)

	state, err := supervisor.Wait(context.Background(), "exec-restack-notstarted", "40")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE", state)
	}
	if rebaser.ontoCalls != 0 {
		t.Fatalf("RebaseOnto calls = %d, want 0 (not-yet-started dependent resolves base via the scheduler's base resolver instead)", rebaser.ontoCalls)
	}
}

func TestWait_PrerequisiteMerges_SkipsTerminalDependent(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-restack-terminal", "40")
	seedDependentIssue(t, store, "exec-restack-terminal", "44",
		[]domain.Dependency{{IssueID: "44", DependsOnID: "40"}},
		domain.StateCancelled,
		"sha-old-44",
		&domain.Workspace{Path: "/tmp/ws-44", Branch: "forge/exec-restack-terminal/44"},
	)

	rebaser := &stubOntoRebaser{}
	pusher := &stubBranchPusher{}
	supervisor := mergedSupervisor(store, rebaser, pusher)

	state, err := supervisor.Wait(context.Background(), "exec-restack-terminal", "40")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE", state)
	}
	if rebaser.ontoCalls != 0 {
		t.Fatalf("RebaseOnto calls = %d, want 0 (a terminal dependent is not in-flight)", rebaser.ontoCalls)
	}
}

func TestWait_PrerequisiteMerges_NoOntoRebaserConfigured_LeavesRestackANoOp(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-restack-noop", "40")
	seedDependentIssue(t, store, "exec-restack-noop", "45",
		[]domain.Dependency{{IssueID: "45", DependsOnID: "40"}},
		domain.StateImplementing,
		"sha-old-45",
		&domain.Workspace{Path: "/tmp/ws-45", Branch: "forge/exec-restack-noop/45"},
	)

	trk := &stubTrackerWithMergeSequence{
		stubTracker: stubTracker{
			mergeRequirements: tracker.MergeRequirements{RequiredChecks: []string{"build"}},
			checkResponses: [][]tracker.PullRequestCheck{
				{{Name: "build", State: tracker.CheckSuccess}},
			},
		},
		statuses: []tracker.PullRequestMergeStatus{{Merged: true}},
	}
	supervisor := ci.New(store, trk, config.Default(), "main")
	supervisor.Now = func() time.Time { return time.Date(2026, 8, 28, 12, 31, 0, 0, time.UTC) }
	// Rebaser only implements ci.Rebaser here, not ci.OntoRebaser (mirrors
	// stubRebaser in staleness_test.go), matching a Tracker/Rebaser pair
	// that predates ticket 332 wiring.
	supervisor.Rebaser = &stubRebaser{}
	supervisor.Pusher = &stubBranchPusher{}

	state, err := supervisor.Wait(context.Background(), "exec-restack-noop", "40")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE", state)
	}
}

func TestWait_PrerequisiteMerges_RestackConflictRoutesDependentToNeedsInfo(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-restack-conflict", "40")
	seedDependentIssue(t, store, "exec-restack-conflict", "46",
		[]domain.Dependency{{IssueID: "46", DependsOnID: "40"}},
		domain.StateImplementing,
		"sha-old-46",
		&domain.Workspace{Path: "/tmp/ws-46", Branch: "forge/exec-restack-conflict/46"},
	)

	rebaser := &stubOntoRebaser{conflicts: []string{"main.go"}}
	pusher := &stubBranchPusher{}
	supervisor := mergedSupervisor(store, rebaser, pusher)

	state, err := supervisor.Wait(context.Background(), "exec-restack-conflict", "40")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE (the merged prerequisite still completes)", state)
	}
	if pusher.calls != 0 {
		t.Fatalf("ForcePush calls = %d, want 0 (a conflicted rebase must not push)", pusher.calls)
	}

	dependent, err := store.GetIssue(context.Background(), "exec-restack-conflict", "46")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if dependent.State != domain.StateNeedsInfo {
		t.Fatalf("dependent state = %s, want NEEDS_INFO", dependent.State)
	}
	if dependent.RetryBudget.CIFailures() != 1 {
		t.Fatalf("dependent CI failures = %d, want 1", dependent.RetryBudget.CIFailures())
	}
	if dependent.RetryBudget.RemainingCI() != 2 {
		t.Fatalf("dependent remaining CI retries = %d, want 2", dependent.RetryBudget.RemainingCI())
	}

	runs, err := store.CIRunsByIssue(context.Background(), "exec-restack-conflict", "46")
	if err != nil {
		t.Fatalf("CIRunsByIssue: %v", err)
	}
	if len(runs) != 1 || runs[0].Kind != storage.CIRunKindConflict || runs[0].Status != storage.CIRunStatusFailed {
		t.Fatalf("runs = %+v, want one FAILED conflict run", runs)
	}
	if !strings.Contains(runs[0].Details, "main.go") {
		t.Fatalf("run details = %q, want the conflicting path", runs[0].Details)
	}
}

func TestWait_PrerequisiteMerges_RestackConflictWithExhaustedBudgetStillRoutesToNeedsInfo(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-restack-exhausted", "40")
	seedDependentIssue(t, store, "exec-restack-exhausted", "47",
		[]domain.Dependency{{IssueID: "47", DependsOnID: "40"}},
		domain.StateImplementing,
		"sha-old-47",
		&domain.Workspace{Path: "/tmp/ws-47", Branch: "forge/exec-restack-exhausted/47"},
	)
	exhaustCIBudget(t, store, "exec-restack-exhausted", "47")

	rebaser := &stubOntoRebaser{conflicts: []string{"main.go"}}
	pusher := &stubBranchPusher{}
	supervisor := mergedSupervisor(store, rebaser, pusher)

	state, err := supervisor.Wait(context.Background(), "exec-restack-exhausted", "40")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE", state)
	}

	dependent, err := store.GetIssue(context.Background(), "exec-restack-exhausted", "47")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if dependent.State != domain.StateNeedsInfo {
		t.Fatalf("dependent state = %s, want NEEDS_INFO", dependent.State)
	}
	if dependent.RetryBudget.CIFailures() != 3 {
		t.Fatalf("dependent CI failures = %d, want 3 (an exhausted budget must not count higher)", dependent.RetryBudget.CIFailures())
	}

	checkpoint, err := store.GetNeedsInfoCheckpoint(context.Background(), "exec-restack-exhausted", "47")
	if err != nil {
		t.Fatalf("GetNeedsInfoCheckpoint: %v", err)
	}
	if !strings.Contains(checkpoint.Context, "retry budget exhausted") {
		t.Fatalf("checkpoint context = %q, want the exhausted retry budget", checkpoint.Context)
	}
}

func TestWait_PrerequisiteMerges_RestackPushFailureRestoresWorkspaceAndRoutesToNeedsInfo(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-restack-push", "40")
	seedDependentIssue(t, store, "exec-restack-push", "48",
		[]domain.Dependency{{IssueID: "48", DependsOnID: "40"}},
		domain.StateImplementing,
		"sha-old-48",
		&domain.Workspace{Path: "/tmp/ws-48", Branch: "forge/exec-restack-push/48"},
	)
	seedDependentPullRequest(t, store, "exec-restack-push", "48", 48, "sha-published-48")

	rebaser := &stubOntoRebaser{}
	pusher := &stubBranchPusher{pushErr: errors.New("remote rejected")}
	resetter := &stubBranchResetter{}
	supervisor := mergedSupervisor(store, rebaser, pusher)
	supervisor.Resetter = resetter

	state, err := supervisor.Wait(context.Background(), "exec-restack-push", "40")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE", state)
	}

	if len(resetter.paths) != 1 || resetter.paths[0] != "/tmp/ws-48" {
		t.Fatalf("Reset paths = %v, want [/tmp/ws-48]", resetter.paths)
	}
	if len(resetter.commitSHAs) != 1 || resetter.commitSHAs[0] != "sha-published-48" {
		t.Fatalf("Reset commit SHAs = %v, want [sha-published-48]", resetter.commitSHAs)
	}

	dependent, err := store.GetIssue(context.Background(), "exec-restack-push", "48")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if dependent.State != domain.StateNeedsInfo {
		t.Fatalf("dependent state = %s, want NEEDS_INFO", dependent.State)
	}
	if dependent.RetryBudget.CIFailures() != 1 {
		t.Fatalf("dependent CI failures = %d, want 1", dependent.RetryBudget.CIFailures())
	}

	checkpoint, err := store.GetNeedsInfoCheckpoint(context.Background(), "exec-restack-push", "48")
	if err != nil {
		t.Fatalf("GetNeedsInfoCheckpoint: %v", err)
	}
	if !strings.Contains(checkpoint.Context, "sha-published-48") {
		t.Fatalf("checkpoint context = %q, want the restored commit", checkpoint.Context)
	}
	if !strings.Contains(checkpoint.Context, "remote rejected") {
		t.Fatalf("checkpoint context = %q, want the push failure", checkpoint.Context)
	}
}

func TestWait_PrerequisiteMerges_RestackPushFailureWithFailedRestorationRoutesToNeedsInfo(t *testing.T) {
	cases := []struct {
		name        string
		resetter    *stubBranchResetter
		executionID string
		issueID     string
		wantDetail  string
	}{
		{
			name:        "reset fails",
			resetter:    &stubBranchResetter{err: errors.New("reset refused")},
			executionID: "exec-restack-reset-failed",
			issueID:     "49",
			wantDetail:  "reset refused",
		},
		{
			name:        "no resetter configured",
			resetter:    nil,
			executionID: "exec-restack-no-resetter",
			issueID:     "50",
			wantDetail:  "no branch resetter",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			seedIssueWithPR(t, store, tc.executionID, "40")
			seedDependentIssue(t, store, tc.executionID, tc.issueID,
				[]domain.Dependency{{IssueID: tc.issueID, DependsOnID: "40"}},
				domain.StateImplementing,
				"sha-old-"+tc.issueID,
				&domain.Workspace{Path: "/tmp/ws-" + tc.issueID, Branch: "forge/" + tc.executionID + "/" + tc.issueID},
			)
			seedDependentPullRequest(t, store, tc.executionID, tc.issueID, 60, "sha-published-"+tc.issueID)

			rebaser := &stubOntoRebaser{}
			pusher := &stubBranchPusher{pushErr: errors.New("remote rejected")}
			supervisor := mergedSupervisor(store, rebaser, pusher)
			if tc.resetter != nil {
				supervisor.Resetter = tc.resetter
			}

			state, err := supervisor.Wait(context.Background(), tc.executionID, "40")
			if err != nil {
				t.Fatalf("Wait: %v", err)
			}
			if state != domain.StateDone {
				t.Fatalf("state = %s, want DONE", state)
			}

			dependent, err := store.GetIssue(context.Background(), tc.executionID, tc.issueID)
			if err != nil {
				t.Fatalf("GetIssue: %v", err)
			}
			if dependent.State != domain.StateNeedsInfo {
				t.Fatalf("dependent state = %s, want NEEDS_INFO", dependent.State)
			}
			if dependent.RetryBudget.CIFailures() != 1 {
				t.Fatalf("dependent CI failures = %d, want 1", dependent.RetryBudget.CIFailures())
			}

			checkpoint, err := store.GetNeedsInfoCheckpoint(context.Background(), tc.executionID, tc.issueID)
			if err != nil {
				t.Fatalf("GetNeedsInfoCheckpoint: %v", err)
			}
			if !strings.Contains(checkpoint.Context, tc.wantDetail) {
				t.Fatalf("checkpoint context = %q, want %q", checkpoint.Context, tc.wantDetail)
			}
		})
	}
}

func TestWait_PrerequisiteMerges_RestackFailureContinuesToNextDependent(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-restack-batch", "40")
	seedDependentIssue(t, store, "exec-restack-batch", "51",
		[]domain.Dependency{{IssueID: "51", DependsOnID: "40"}},
		domain.StateImplementing,
		"sha-old-51",
		&domain.Workspace{Path: "/tmp/ws-51", Branch: "forge/exec-restack-batch/51"},
	)
	seedDependentIssue(t, store, "exec-restack-batch", "52",
		[]domain.Dependency{{IssueID: "52", DependsOnID: "40"}},
		domain.StateImplementing,
		"sha-old-52",
		&domain.Workspace{Path: "/tmp/ws-52", Branch: "forge/exec-restack-batch/52"},
	)

	// The first dependent conflicts, the second rebases cleanly.
	rebaser := &stubOntoRebaser{conflictsByIssue: map[string][]string{"51": {"main.go"}}}
	pusher := &stubBranchPusher{}
	supervisor := mergedSupervisor(store, rebaser, pusher)

	state, err := supervisor.Wait(context.Background(), "exec-restack-batch", "40")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if state != domain.StateDone {
		t.Fatalf("state = %s, want DONE", state)
	}
	if rebaser.ontoCalls != 2 {
		t.Fatalf("RebaseOnto calls = %d, want 2 (a failed dependent must not stop the batch)", rebaser.ontoCalls)
	}
	if pusher.calls != 1 || pusher.paths[0] != "/tmp/ws-52" {
		t.Fatalf("ForcePush calls = %d paths = %v, want one push of /tmp/ws-52", pusher.calls, pusher.paths)
	}

	first, err := store.GetIssue(context.Background(), "exec-restack-batch", "51")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if first.State != domain.StateNeedsInfo {
		t.Fatalf("dependent 51 state = %s, want NEEDS_INFO", first.State)
	}
	second, err := store.GetIssue(context.Background(), "exec-restack-batch", "52")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if second.State != domain.StateImplementing {
		t.Fatalf("dependent 52 state = %s, want IMPLEMENTING (a clean restack changes no state)", second.State)
	}
}

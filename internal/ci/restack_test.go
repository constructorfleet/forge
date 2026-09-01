package ci_test

import (
	"context"
	"encoding/json"
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
	err         error
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
	return s.conflicts, s.err
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

func TestWait_PrerequisiteMerges_RestackConflictReturnsError(t *testing.T) {
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

	if _, err := supervisor.Wait(context.Background(), "exec-restack-conflict", "40"); err == nil {
		t.Fatalf("Wait: want error on restack conflict (conflict routing is ticket 4), got nil")
	}
	if pusher.calls != 0 {
		t.Fatalf("ForcePush calls = %d, want 0 (conflicted rebase must not push)", pusher.calls)
	}
}

package tui_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
)

// fakeRosterStore is a scripted RosterStore double: it holds the Execution
// state it LoadExecution returns and a per-Issue WorkerClaim lookup, so a
// test can drive the poller's whole state-fetch pass deterministically.
type fakeRosterStore struct {
	state   storage.ExecutionState
	claims  map[string]storage.WorkerClaim
	claimOK map[string]bool
	loadErr error
	// blockLoad holds LoadExecution until it closes.
	blockLoad chan struct{}

	// reviews holds the Review history per Issue, in insertion order.
	reviews map[string][]storage.ReviewRun
	// runReads counts LatestReviewDiff calls, so a test can prove a poll pass
	// never reads the diff blobs.
	runReads int
	// blockDiff holds LatestReviewDiff until it closes, so a test can prove a
	// slow diff read never blocks the update goroutine.
	blockDiff chan struct{}

	// checkpoints holds the replan checkpoint per Issue, for the approve key's
	// on-request read (see approve_test.go).
	checkpoints map[string]storage.ReplanCheckpoint

	// needsInfoCheckpoints holds the needs-info checkpoint per Issue, for the
	// answer key's on-request read (see answer_test.go).
	needsInfoCheckpoints map[string]storage.NeedsInfoCheckpoint

	// agentRuns holds the AgentRuns per Issue, so the roster's attempt count
	// can be proven to derive from the same source the transcript pane
	// numbers its "attempt N" divider from.
	agentRuns map[string][]storage.AgentRun
}

func (f *fakeRosterStore) GetReplanCheckpoint(_ context.Context, _, issueID string) (storage.ReplanCheckpoint, error) {
	checkpoint, ok := f.checkpoints[issueID]
	if !ok {
		return storage.ReplanCheckpoint{}, storage.ErrNotFound
	}
	return checkpoint, nil
}

func (f *fakeRosterStore) GetNeedsInfoCheckpoint(_ context.Context, _, issueID string) (storage.NeedsInfoCheckpoint, error) {
	checkpoint, ok := f.needsInfoCheckpoints[issueID]
	if !ok {
		return storage.NeedsInfoCheckpoint{}, storage.ErrNotFound
	}
	return checkpoint, nil
}

func (f *fakeRosterStore) LatestReviewOutcome(_ context.Context, _, issueID string) (storage.ReviewOutcome, error) {
	runs := f.reviews[issueID]
	if len(runs) == 0 {
		return storage.ReviewOutcome{}, nil
	}
	last := runs[len(runs)-1]
	return storage.ReviewOutcome{Verdict: last.Verdict, HasDiff: last.Diff != "", Recorded: true}, nil
}

func (f *fakeRosterStore) LatestReviewDiff(_ context.Context, _, issueID string) (string, error) {
	f.runReads++
	if f.blockDiff != nil {
		<-f.blockDiff
	}
	runs := f.reviews[issueID]
	if len(runs) == 0 {
		return "", nil
	}
	return runs[len(runs)-1].Diff, nil
}

func (f *fakeRosterStore) AgentRunsByIssue(_ context.Context, _, issueID string) ([]storage.AgentRun, error) {
	return f.agentRuns[issueID], nil
}

func (f *fakeRosterStore) LoadExecution(context.Context, string) (storage.ExecutionState, error) {
	if f.blockLoad != nil {
		<-f.blockLoad
	}
	if f.loadErr != nil {
		return storage.ExecutionState{}, f.loadErr
	}
	return f.state, nil
}

func (f *fakeRosterStore) WorkerClaim(_ context.Context, _, issueID string) (storage.WorkerClaim, error) {
	if f.claimOK[issueID] {
		return f.claims[issueID], nil
	}
	return storage.WorkerClaim{}, storage.ErrNotFound
}

func mustRetryBudget(limits domain.RetryLimits, gate, review, ci, provider int) domain.RetryBudget {
	return domain.NewRetryBudgetFrom(limits, gate, review, ci, provider)
}

// TestRosterFetchBuildsVMFromStoreAndClock proves one poller pass turns the
// store's Issue + WorkerClaim state, resolved against the injected clock,
// into the view-model the frame renders: elapsed from state_changed_at and a
// truthful liveness badge from last_heartbeat, kept the distinct quantities
// the liveness criterion demands.
func TestRosterFetchBuildsVMFromStoreAndClock(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				// Living Worker: a fresh beat inside the 15s stale window.
				{
					ID: "#1", Title: "Write tests", State: domain.StateImplementing,
					StateChangedAt: now.Add(-30 * time.Second),
					RetryBudget:    mustRetryBudget(domain.RetryLimits{Gate: 3}, 1, 0, 0, 0),
				},
				// Wedged Worker: a beat far beyond the stale window.
				{
					ID: "#2", Title: "Add roster", State: domain.StateImplementing,
					StateChangedAt: now.Add(-2 * time.Minute),
					RetryBudget:    mustRetryBudget(domain.RetryLimits{Gate: 3}, 0, 0, 0, 0),
				},
				// Planning row: no claim, so no liveness claim at all.
				{
					ID: "#3", Title: "Pending work", State: domain.StatePending,
					StateChangedAt: now.Add(-time.Second),
				},
			},
		},
		claimOK: map[string]bool{"#1": true, "#2": true},
		claims: map[string]storage.WorkerClaim{
			"#1": {LastHeartbeat: now.Add(-3 * time.Second)},
			"#2": {LastHeartbeat: now.Add(-2 * time.Minute)},
		},
	}

	r := tui.NewRoster(store, func() time.Time { return now })

	vm, err := r.Fetch(context.Background(), "ex-1", now)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(vm.Workers) != 3 {
		t.Fatalf("len(vm.Workers) = %d, want 3", len(vm.Workers))
	}

	// Row #1: living worker shows a live badge; elapsed is 30s, distinct from
	// the 3s heartbeat age.
	row := vm.Workers[0]
	if row.IssueID != "#1" || row.Title != "Write tests" || row.State != domain.StateImplementing {
		t.Fatalf("row 0 identity = %+v, want #1/Write tests/IMPLEMENTING", row)
	}
	if !row.HasHeartbeat {
		t.Fatalf("row 0 HasHeartbeat = false, want true")
	}
	if row.Elapsed != 30*time.Second {
		t.Fatalf("row 0 Elapsed = %s, want 30s (state_changed_at 30s ago)", row.Elapsed)
	}
	if row.HeartbeatAge != 3*time.Second {
		t.Fatalf("row 0 HeartbeatAge = %s, want 3s (last_heartbeat 3s ago)", row.HeartbeatAge)
	}

	// Row #2: wedged worker's heartbeat is beyond the stale window; elapsed
	// and heartbeat age must render as distinct quantities.
	row = vm.Workers[1]
	if !row.HasHeartbeat {
		t.Fatalf("row 1 HasHeartbeat = false, want true")
	}
	if row.Elapsed != 2*time.Minute {
		t.Fatalf("row 1 Elapsed = %s, want 2m (state_changed_at 2m ago)", row.Elapsed)
	}
	if row.HeartbeatAge != 2*time.Minute {
		t.Fatalf("row 1 HeartbeatAge = %s, want 2m (last_heartbeat 2m ago)", row.HeartbeatAge)
	}
	if got := tui.DeriveLiveness(row.HasHeartbeat, row.HeartbeatAge); got != tui.LivenessStale {
		t.Fatalf("row 1 liveness = %v, want stale", got)
	}

	// Row #3: planning row claims no liveness without a beat.
	row = vm.Workers[2]
	if row.HasHeartbeat {
		t.Fatalf("row 2 HasHeartbeat = true, want false (no worker claim)")
	}
}

// TestRosterFetchAttemptMatchesAgentRunCount proves the roster's "attempt N"
// counts the Issue's recorded AgentRuns — the same count the transcript
// pane's own "── attempt N ──" divider numbers from — rather than a rolled-up
// retry-budget failure tally that can drift out of step with it (e.g. a
// lost-execution recovery restarts the Agent, adding an AgentRun, without
// recording any gate/review/CI failure).
func TestRosterFetchAttemptMatchesAgentRunCount(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{
					ID: "#1", Title: "Write tests", State: domain.StateImplementing,
					// No recorded failure at all, yet a lost-execution recovery
					// already restarted the Agent twice: three AgentRuns exist.
					RetryBudget: mustRetryBudget(domain.RetryLimits{Gate: 3}, 0, 0, 0, 0),
				},
			},
		},
		agentRuns: map[string][]storage.AgentRun{
			"#1": {{ID: 1, IssueID: "#1"}, {ID: 2, IssueID: "#1"}, {ID: 3, IssueID: "#1"}},
		},
	}
	r := tui.NewRoster(store, func() time.Time { return now })

	vm, err := r.Fetch(context.Background(), "ex-1", now)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := vm.Workers[0].Attempt; got != 3 {
		t.Errorf("Attempt = %d, want 3 (three recorded AgentRuns, matching the transcript's own attempt divider), got budget failures alone", got)
	}
}

// TestRosterFetchDerivesLiveBadgeFromClock proves DeriveLiveness renders the
// injected clock's heartbeat age against the 15s stale window.
func TestRosterFetchDerivesLiveBadgeFromClock(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "t", State: domain.StateImplementing},
			},
		},
		claimOK: map[string]bool{"#1": true},
		claims:  map[string]storage.WorkerClaim{"#1": {LastHeartbeat: now.Add(-10 * time.Second)}},
	}
	r := tui.NewRoster(store, func() time.Time { return now })

	vm, err := r.Fetch(context.Background(), "ex-1", now)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := tui.DeriveLiveness(vm.Workers[0].HasHeartbeat, vm.Workers[0].HeartbeatAge); got != tui.LivenessLive {
		t.Fatalf("liveness = %v, want live (10s within 15s window)", got)
	}
}

// TestRosterFetchPropagatesStoreError proves a failed store read surfaces as
// an error from Fetch rather than being swallowed into a partial view.
func TestRosterFetchPropagatesStoreError(t *testing.T) {
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues:    []domain.Issue{{ID: "#1", Title: "t"}},
		},
		claimOK: map[string]bool{"#1": false},
	}
	// A claim-error propagation: make LoadExecution fail instead by wrapping.
	failing := &failingLoadRosterStore{fake: store}
	r := tui.NewRoster(failing, func() time.Time { return time.Now() })

	if _, err := r.Fetch(context.Background(), "ex-1", time.Now()); err == nil {
		t.Fatal("Fetch: want error from failed LoadExecution, got nil")
	}
}

// TestRosterFetchAwaitsMissingExecution proves the roster, which starts before
// the Scheduler writes the Execution, reports a wait notice instead of an error.
func TestRosterFetchAwaitsMissingExecution(t *testing.T) {
	r := tui.NewRoster(&missingExecutionRosterStore{}, time.Now)

	vm, err := r.Fetch(context.Background(), "ex-1", time.Now())
	if err != nil {
		t.Fatalf("Fetch: want nil error for a missing execution, got %v", err)
	}
	if len(vm.Workers) != 0 {
		t.Fatalf("Fetch: want no rows, got %d", len(vm.Workers))
	}
	if vm.Notice == "" {
		t.Fatal("Fetch: want a notice explaining the empty roster")
	}
}

type missingExecutionRosterStore struct{}

func (missingExecutionRosterStore) AgentRunsByIssue(context.Context, string, string) ([]storage.AgentRun, error) {
	return nil, nil
}

func (missingExecutionRosterStore) LoadExecution(context.Context, string) (storage.ExecutionState, error) {
	return storage.ExecutionState{}, storage.ErrNotFound
}

func (missingExecutionRosterStore) WorkerClaim(_ context.Context, _, _ string) (storage.WorkerClaim, error) {
	return storage.WorkerClaim{}, storage.ErrNotFound
}

func (missingExecutionRosterStore) LatestReviewDiff(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func (missingExecutionRosterStore) LatestReviewOutcome(_ context.Context, _, _ string) (storage.ReviewOutcome, error) {
	return storage.ReviewOutcome{}, nil
}

func (missingExecutionRosterStore) GetReplanCheckpoint(_ context.Context, _, _ string) (storage.ReplanCheckpoint, error) {
	return storage.ReplanCheckpoint{}, storage.ErrNotFound
}

func (missingExecutionRosterStore) GetNeedsInfoCheckpoint(_ context.Context, _, _ string) (storage.NeedsInfoCheckpoint, error) {
	return storage.NeedsInfoCheckpoint{}, storage.ErrNotFound
}

type failingLoadRosterStore struct {
	fake *fakeRosterStore
}

func (f *failingLoadRosterStore) AgentRunsByIssue(context.Context, string, string) ([]storage.AgentRun, error) {
	return nil, nil
}

func (f *failingLoadRosterStore) LoadExecution(context.Context, string) (storage.ExecutionState, error) {
	return storage.ExecutionState{}, errors.New("load failed")
}

func (f *failingLoadRosterStore) WorkerClaim(_ context.Context, _, _ string) (storage.WorkerClaim, error) {
	return storage.WorkerClaim{}, storage.ErrNotFound
}

func (f *failingLoadRosterStore) LatestReviewDiff(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func (f *failingLoadRosterStore) LatestReviewOutcome(_ context.Context, _, _ string) (storage.ReviewOutcome, error) {
	return storage.ReviewOutcome{}, nil
}

func (f *failingLoadRosterStore) GetReplanCheckpoint(_ context.Context, _, _ string) (storage.ReplanCheckpoint, error) {
	return storage.ReplanCheckpoint{}, storage.ErrNotFound
}

func (f *failingLoadRosterStore) GetNeedsInfoCheckpoint(_ context.Context, _, _ string) (storage.NeedsInfoCheckpoint, error) {
	return storage.NeedsInfoCheckpoint{}, storage.ErrNotFound
}

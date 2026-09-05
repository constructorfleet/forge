package storage_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

func openTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "forge.db")
	store, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

func TestMigrateCreatesAllTables(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// executions, execution_issues, and dependencies are exercised via the
	// Store interface elsewhere; seed the same fixture here so the
	// remaining tables (workers, workspaces, agent_runs, gate_runs,
	// review_runs, review_findings, pull_requests, ci_runs, events) can be
	// asserted directly by name.
	exec := domain.Execution{ID: "exec-schema", BaseRevision: "abc123", StartedAt: time.Now()}
	if err := store.CreateExecution(ctx, exec); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	issue := domain.Issue{
		ID: "issue-1", ExecutionID: exec.ID,
		State: domain.StatePending, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	tables := []string{
		"executions", "execution_issues", "dependencies", "workers",
		"workspaces", "agent_runs", "gate_runs", "review_runs",
		"review_findings", "review_axis_envelopes", "pull_requests", "ci_runs", "events",
		"schema_migrations",
	}
	for _, table := range tables {
		exists, err := store.TableExists(ctx, table)
		if err != nil {
			t.Errorf("table %s: %v", table, err)
			continue
		}
		if !exists {
			t.Errorf("table %s: does not exist", table)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate call: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("third Migrate call: %v", err)
	}
}

func TestExecutionRoundTrip(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	exec := domain.Execution{
		ID:           "exec-1",
		BaseRevision: "deadbeef",
		StartedAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	if err := store.CreateExecution(ctx, exec); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	issue := domain.Issue{
		ID:          "issue-1",
		Provider:    "linear",
		ExecutionID: exec.ID,
		Title:       "Add widget support",
		Body:        "Widgets should render before gadgets.",
		State:       domain.StateReady,
		Scope:       domain.ScopeManaged,
		Dependencies: []domain.Dependency{
			{
				IssueID:      "issue-1",
				DependsOnID:  "issue-0",
				IssueRef:     domain.IssueRef{Provider: "linear", ID: "issue-1"},
				DependsOnRef: domain.IssueRef{Provider: "linear", ID: "issue-0"},
			},
		},
		RetryBudget: domain.NewRetryBudgetFrom(domain.RetryLimits{Gate: 2, Review: 2, CI: 2, ProviderLimit: 2}, 1, 0, 0, 0),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	loaded, err := store.LoadExecution(ctx, exec.ID)
	if err != nil {
		t.Fatalf("LoadExecution: %v", err)
	}

	if loaded.Execution.ID != exec.ID || loaded.Execution.BaseRevision != exec.BaseRevision {
		t.Fatalf("execution mismatch: got %+v, want %+v", loaded.Execution, exec)
	}
	if !loaded.Execution.StartedAt.Equal(exec.StartedAt) {
		t.Fatalf("StartedAt mismatch: got %v, want %v", loaded.Execution.StartedAt, exec.StartedAt)
	}
	if len(loaded.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(loaded.Issues))
	}

	got := loaded.Issues[0]
	if got.ID != issue.ID || got.State != issue.State || got.Scope != issue.Scope {
		t.Fatalf("issue mismatch: got %+v", got)
	}
	if got.Title != issue.Title || got.Body != issue.Body {
		t.Fatalf("issue title/body mismatch: got %+v", got)
	}
	if got.Provider != "linear" {
		t.Fatalf("issue provider mismatch: got %q, want linear", got.Provider)
	}
	if len(got.Dependencies) != 1 || got.Dependencies[0].DependsOnID != "issue-0" {
		t.Fatalf("dependencies mismatch: got %+v", got.Dependencies)
	}
	if got.Dependencies[0].IssueRef != (domain.IssueRef{Provider: "linear", ID: "issue-1"}) {
		t.Fatalf("dependency issue ref mismatch: got %+v", got.Dependencies[0])
	}
	if got.Dependencies[0].DependsOnRef != (domain.IssueRef{Provider: "linear", ID: "issue-0"}) {
		t.Fatalf("dependency depends-on ref mismatch: got %+v", got.Dependencies[0])
	}
	if got.RetryBudget.GateFailures() != 1 || got.RetryBudget.RemainingGate() != 1 {
		t.Fatalf("retry budget mismatch: got %+v", got.RetryBudget)
	}
}

func TestListExecutions_ReloadsIssuesForEachExecution(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	for _, executionID := range []string{"exec-a", "exec-b"} {
		if err := store.CreateExecution(ctx, domain.Execution{
			ID:           executionID,
			BaseRevision: "base-" + executionID,
			StartedAt:    time.Now().UTC(),
		}); err != nil {
			t.Fatalf("CreateExecution(%s): %v", executionID, err)
		}
		if err := store.CreateIssue(ctx, domain.Issue{
			ID:          "issue-" + executionID,
			ExecutionID: executionID,
			State:       domain.StateReady,
			Scope:       domain.ScopeManaged,
			RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
		}); err != nil {
			t.Fatalf("CreateIssue(%s): %v", executionID, err)
		}
	}

	states, err := store.ListExecutions(ctx)
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("got %d executions, want 2", len(states))
	}
	for _, state := range states {
		if len(state.Issues) != 1 {
			t.Fatalf("execution %s issues = %+v, want one issue", state.Execution.ID, state.Issues)
		}
	}
}

func seedExecutionAndIssue(t *testing.T, store *storage.SQLiteStore, executionID, issueID string, state domain.IssueState) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{ID: executionID, BaseRevision: "base", StartedAt: time.Now()}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	issue := domain.Issue{
		ID: issueID, ExecutionID: executionID, State: state, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
}

func TestTransitionIssuePersistsAndEmitsEvent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StatePending)

	got, err := store.TransitionIssue(ctx, "exec-1", "issue-1", domain.StateReady)
	if err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	if got.State != domain.StateReady {
		t.Fatalf("expected READY, got %s", got.State)
	}

	reloaded, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if reloaded.State != domain.StateReady {
		t.Fatalf("persisted state mismatch: got %s", reloaded.State)
	}

	events, err := store.EventsByIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	if len(events) != 1 || events[0].Type != "issue.transitioned" {
		t.Fatalf("expected 1 transition event, got %+v", events)
	}
}

func TestTransitionIssueRejectsIllegalTransition(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StatePending)

	// PENDING -> DONE is not a legal transition.
	_, err := store.TransitionIssue(ctx, "exec-1", "issue-1", domain.StateDone)
	var invalidErr *domain.InvalidTransitionError
	if !errors.As(err, &invalidErr) {
		t.Fatalf("expected *domain.InvalidTransitionError, got %v (%T)", err, err)
	}

	// State must be left unchanged and no event should have been recorded.
	reloaded, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if reloaded.State != domain.StatePending {
		t.Fatalf("state should be unchanged, got %s", reloaded.State)
	}

	events, err := store.EventsByIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no events from rejected transition, got %+v", events)
	}
}

func TestClaimIssuePreventsDuplicateClaims(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StateReady)

	if err := store.ClaimIssue(ctx, "exec-1", "issue-1", "worker-a"); err != nil {
		t.Fatalf("first ClaimIssue: %v", err)
	}

	err := store.ClaimIssue(ctx, "exec-1", "issue-1", "worker-b")
	if !errors.Is(err, storage.ErrAlreadyClaimed) {
		t.Fatalf("expected ErrAlreadyClaimed, got %v", err)
	}

	events, err := store.EventsByIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	if len(events) != 1 || events[0].Type != "issue.claimed" {
		t.Fatalf("expected exactly 1 claim event, got %+v", events)
	}
}

func TestClaimIssue_RejectsClaimHeldByAnotherExecution(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StateReady)
	seedExecutionAndIssue(t, store, "exec-2", "issue-1", domain.StateReady)

	if err := store.ClaimIssue(ctx, "exec-1", "issue-1", "worker-a"); err != nil {
		t.Fatalf("first ClaimIssue: %v", err)
	}

	err := store.ClaimIssue(ctx, "exec-2", "issue-1", "worker-b")
	if !errors.Is(err, storage.ErrAlreadyClaimed) {
		t.Fatalf("expected ErrAlreadyClaimed, got %v", err)
	}

	var conflict *storage.ClaimConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected ClaimConflictError, got %T", err)
	}
	if conflict.OwningExecutionID != "exec-1" {
		t.Fatalf("OwningExecutionID = %q, want exec-1", conflict.OwningExecutionID)
	}
	if conflict.IssueID != "issue-1" {
		t.Fatalf("IssueID = %q, want issue-1", conflict.IssueID)
	}
}

func TestClaimIssueRejectsNonexistentIssue(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{ID: "exec-1", BaseRevision: "base", StartedAt: time.Now()}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	// No CreateIssue call: issue-ghost was never persisted.
	err := store.ClaimIssue(ctx, "exec-1", "issue-ghost", "worker-a")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	events, err := store.EventsByIssue(ctx, "exec-1", "issue-ghost")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no events for a rejected claim, got %+v", events)
	}
}

func TestEventLogQueryableByExecutionIssueAndTimeRange(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{ID: "exec-1", BaseRevision: "base", StartedAt: time.Now()}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	for _, issueID := range []string{"issue-1", "issue-2"} {
		issue := domain.Issue{
			ID: issueID, ExecutionID: "exec-1", State: domain.StatePending, Scope: domain.ScopeManaged,
			RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
		}
		if err := store.CreateIssue(ctx, issue); err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []storage.Event{
		{ExecutionID: "exec-1", IssueID: "issue-1", Type: "note", Data: "{}", OccurredAt: base},
		{ExecutionID: "exec-1", IssueID: "issue-2", Type: "note", Data: "{}", OccurredAt: base.Add(time.Hour)},
		{ExecutionID: "exec-1", Type: "execution.note", Data: "{}", OccurredAt: base.Add(2 * time.Hour)},
	}
	for _, e := range events {
		if err := store.AppendEvent(ctx, e); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	byExecution, err := store.EventsByExecution(ctx, "exec-1")
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	if len(byExecution) != 3 {
		t.Fatalf("expected 3 events for execution, got %d", len(byExecution))
	}

	byIssue, err := store.EventsByIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	if len(byIssue) != 1 || byIssue[0].IssueID != "issue-1" {
		t.Fatalf("expected 1 event for issue-1, got %+v", byIssue)
	}

	byRange, err := store.EventsByTimeRange(ctx, "exec-1", base.Add(30*time.Minute), base.Add(90*time.Minute))
	if err != nil {
		t.Fatalf("EventsByTimeRange: %v", err)
	}
	if len(byRange) != 1 || byRange[0].IssueID != "issue-2" {
		t.Fatalf("expected 1 event (issue-2) in range, got %+v", byRange)
	}
}

func TestStateSurvivesProcessRestart(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "forge.db")

	store, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	exec := domain.Execution{ID: "exec-restart", BaseRevision: "base", StartedAt: time.Now()}
	if err := store.CreateExecution(ctx, exec); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	issue := domain.Issue{
		ID: "issue-1", ExecutionID: exec.ID, State: domain.StatePending, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := store.TransitionIssue(ctx, exec.ID, issue.ID, domain.StateReady); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	if err := store.ClaimIssue(ctx, exec.ID, issue.ID, "worker-a"); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}

	// Simulate a process restart: close the database and reopen it fresh.
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("reopen Open: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Migrate(ctx); err != nil {
		t.Fatalf("reopen Migrate: %v", err)
	}

	state, err := reopened.LoadExecution(ctx, exec.ID)
	if err != nil {
		t.Fatalf("LoadExecution after restart: %v", err)
	}
	if len(state.Issues) != 1 || state.Issues[0].State != domain.StateReady {
		t.Fatalf("state not intact after restart: %+v", state)
	}

	// The duplicate-claim constraint must still hold post-restart.
	err = reopened.ClaimIssue(ctx, exec.ID, issue.ID, "worker-b")
	if !errors.Is(err, storage.ErrAlreadyClaimed) {
		t.Fatalf("expected ErrAlreadyClaimed after restart, got %v", err)
	}

	events, err := reopened.EventsByExecution(ctx, exec.ID)
	if err != nil {
		t.Fatalf("EventsByExecution after restart: %v", err)
	}
	if len(events) != 2 { // transition + claim
		t.Fatalf("expected 2 events to survive restart, got %d: %+v", len(events), events)
	}
}

// TestClaimIssueRecordsInitialHeartbeat pins that claiming a Worker seeds
// workers.last_heartbeat (the Worker is alive from the moment it claims),
// so WorkerClaim reflects a non-zero heartbeat without an explicit beat.
func TestClaimIssueRecordsInitialHeartbeat(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StateReady)

	if err := store.ClaimIssue(ctx, "exec-1", "issue-1", "worker-a"); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
	claim, err := store.WorkerClaim(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("WorkerClaim: %v", err)
	}
	if claim.LastHeartbeat.IsZero() {
		t.Fatal("expected claim to seed a non-zero LastHeartbeat")
	}
}

// TestHeartbeatWorkerAdvancesLastHeartbeat pins the heartbeat write: an
// explicit beat stamps the claim's LastHeartbeat with the supplied time.
func TestHeartbeatWorkerAdvancesLastHeartbeat(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StateReady)
	if err := store.ClaimIssue(ctx, "exec-1", "issue-1", "worker-a"); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}

	at := time.Unix(1710000000, 0).UTC()
	if err := store.HeartbeatWorker(ctx, "exec-1", "issue-1", at); err != nil {
		t.Fatalf("HeartbeatWorker: %v", err)
	}
	claim, err := store.WorkerClaim(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("WorkerClaim: %v", err)
	}
	if !claim.LastHeartbeat.Equal(at) {
		t.Fatalf("LastHeartbeat = %v, want %v", claim.LastHeartbeat, at)
	}
}

// TestTransitionIssueRecordsStateChangedAt pins that the transition
// transaction stamps execution_issues.state_changed_at, so a reloaded Issue
// carries a non-zero StateChangedAt. Verifies both the returned Issue and
// the persisted round-trip.
func TestTransitionIssueRecordsStateChangedAt(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StatePending)

	got, err := store.TransitionIssue(ctx, "exec-1", "issue-1", domain.StateReady)
	if err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	if got.StateChangedAt.IsZero() {
		t.Fatal("expected returned Issue to have a non-zero StateChangedAt")
	}

	reloaded, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if reloaded.StateChangedAt.IsZero() {
		t.Fatal("expected persisted Issue to have a non-zero StateChangedAt")
	}
	if !reloaded.StateChangedAt.Equal(got.StateChangedAt) {
		t.Fatalf("StateChangedAt mismatch: %v vs %v", reloaded.StateChangedAt, got.StateChangedAt)
	}
}

// TestClearWorkerOwnerZeroesOwnerColumnsForPID pins issue 563: a clean
// process shutdown zeroes owner_pid and owner_token for every claim the
// exiting pid owns, rather than leaving a stale pid discoverable until the
// claim is separately released.
func TestClearWorkerOwnerZeroesOwnerColumnsForPID(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StateReady)

	if err := store.ClaimIssue(ctx, "exec-1", "issue-1", "worker-a"); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
	if err := store.UpdateWorkerOwner(ctx, "exec-1", "issue-1", 4242, "token-a"); err != nil {
		t.Fatalf("UpdateWorkerOwner: %v", err)
	}

	if err := store.ClearWorkerOwner(ctx, 4242); err != nil {
		t.Fatalf("ClearWorkerOwner: %v", err)
	}

	claim, err := store.WorkerClaim(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("WorkerClaim: %v", err)
	}
	if claim.OwnerPID != 0 {
		t.Fatalf("OwnerPID = %d, want 0", claim.OwnerPID)
	}
	if claim.OwnerToken != "" {
		t.Fatalf("OwnerToken = %q, want empty", claim.OwnerToken)
	}
}

// TestClearWorkerOwnerLeavesOtherPIDsUntouched pins that clearing one pid's
// owned claims does not disturb a claim owned by a different, still-live
// process.
func TestClearWorkerOwnerLeavesOtherPIDsUntouched(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StateReady)

	if err := store.ClaimIssue(ctx, "exec-1", "issue-1", "worker-a"); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
	if err := store.UpdateWorkerOwner(ctx, "exec-1", "issue-1", 4242, "token-a"); err != nil {
		t.Fatalf("UpdateWorkerOwner: %v", err)
	}

	if err := store.ClearWorkerOwner(ctx, 9999); err != nil {
		t.Fatalf("ClearWorkerOwner: %v", err)
	}

	claim, err := store.WorkerClaim(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("WorkerClaim: %v", err)
	}
	if claim.OwnerPID != 4242 {
		t.Fatalf("OwnerPID = %d, want 4242", claim.OwnerPID)
	}
	if claim.OwnerToken != "token-a" {
		t.Fatalf("OwnerToken = %q, want %q", claim.OwnerToken, "token-a")
	}
}

package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

// seedProviderLimitIssue creates an Execution and one Issue in it, with the
// given state and provider-limit budget.
func seedProviderLimitIssue(t *testing.T, store *storage.SQLiteStore, executionID, issueID string, state domain.IssueState, limit, used int) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{ID: executionID, BaseRevision: "base", StartedAt: time.Now().UTC()}); err != nil {
		// A shared Execution across several Issues is created once.
		if _, loadErr := store.LoadExecution(ctx, executionID); loadErr != nil {
			t.Fatalf("CreateExecution: %v", err)
		}
	}
	issue := domain.Issue{
		ID: issueID, ExecutionID: executionID, State: state, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudgetFrom(
			domain.RetryLimits{Gate: 3, Review: 3, CI: 3, ProviderLimit: limit},
			0, 0, 0, used,
		),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
}

// TestProviderLimitBudgetRoundTripsThroughStorage proves CreateIssue writes
// and GetIssue reads the provider-limit ceiling and count.
func TestProviderLimitBudgetRoundTripsThroughStorage(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedProviderLimitIssue(t, store, "exec-1", "issue-1", domain.StateReady, 4, 2)

	got, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.RetryBudget.Limits().ProviderLimit != 4 {
		t.Fatalf("provider-limit ceiling = %d, want 4", got.RetryBudget.Limits().ProviderLimit)
	}
	if got.RetryBudget.ProviderLimitFailures() != 2 {
		t.Fatalf("provider-limit stops = %d, want 2", got.RetryBudget.ProviderLimitFailures())
	}
	if got.ProviderLimitRetryAt != nil {
		t.Fatalf("ProviderLimitRetryAt = %v, want nil", got.ProviderLimitRetryAt)
	}

	listed, err := store.ListIssues(ctx, "exec-1")
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(listed) != 1 || listed[0].RetryBudget.ProviderLimitFailures() != 2 {
		t.Fatalf("ListIssues did not round-trip the provider-limit count: %+v", listed)
	}
}

// TestUpdateRetryBudgetPersistsProviderLimitStops proves the fourth counter
// survives the reload TransitionIssue performs.
func TestUpdateRetryBudgetPersistsProviderLimitStops(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedProviderLimitIssue(t, store, "exec-1", "issue-1", domain.StateReady, 3, 0)

	issue, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if err := issue.RecordProviderLimitStop(); err != nil {
		t.Fatalf("RecordProviderLimitStop: %v", err)
	}
	if err := store.UpdateRetryBudget(ctx, "exec-1", "issue-1", issue.RetryBudget); err != nil {
		t.Fatalf("UpdateRetryBudget: %v", err)
	}

	reloaded, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue after update: %v", err)
	}
	if reloaded.RetryBudget.ProviderLimitFailures() != 1 {
		t.Fatalf("provider-limit stops = %d, want 1", reloaded.RetryBudget.ProviderLimitFailures())
	}
}

// TestScheduleProviderLimitRetryRoundTrips proves the backoff deadline
// persists and can be cleared again.
func TestScheduleProviderLimitRetryRoundTrips(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedProviderLimitIssue(t, store, "exec-1", "issue-1", domain.StateReady, 3, 0)

	due := time.Date(2026, 9, 2, 12, 30, 0, 0, time.UTC)
	if err := store.ScheduleProviderLimitRetry(ctx, "exec-1", "issue-1", &due); err != nil {
		t.Fatalf("ScheduleProviderLimitRetry: %v", err)
	}
	got, err := store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.ProviderLimitRetryAt == nil || !got.ProviderLimitRetryAt.Equal(due) {
		t.Fatalf("ProviderLimitRetryAt = %v, want %v", got.ProviderLimitRetryAt, due)
	}

	if err := store.ScheduleProviderLimitRetry(ctx, "exec-1", "issue-1", nil); err != nil {
		t.Fatalf("ScheduleProviderLimitRetry(nil): %v", err)
	}
	got, err = store.GetIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("GetIssue after clear: %v", err)
	}
	if got.ProviderLimitRetryAt != nil {
		t.Fatalf("ProviderLimitRetryAt = %v, want nil after clear", got.ProviderLimitRetryAt)
	}
}

// TestListDueProviderLimitIssues proves the cross-Execution query returns
// only Issues that are in PROVIDER_LIMIT and whose backoff time has passed.
func TestListDueProviderLimitIssues(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	seedProviderLimitIssue(t, store, "exec-a", "issue-due", domain.StateProviderLimit, 3, 1)
	seedProviderLimitIssue(t, store, "exec-a", "issue-not-due", domain.StateProviderLimit, 3, 1)
	seedProviderLimitIssue(t, store, "exec-b", "issue-other-exec", domain.StateProviderLimit, 3, 1)
	seedProviderLimitIssue(t, store, "exec-b", "issue-unscheduled", domain.StateProviderLimit, 3, 1)
	seedProviderLimitIssue(t, store, "exec-b", "issue-implementing", domain.StateImplementing, 3, 1)

	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)
	for _, sched := range []struct {
		executionID, issueID string
		at                   time.Time
	}{
		{"exec-a", "issue-due", past},
		{"exec-a", "issue-not-due", future},
		{"exec-b", "issue-other-exec", past},
		{"exec-b", "issue-implementing", past},
	} {
		at := sched.at
		if err := store.ScheduleProviderLimitRetry(ctx, sched.executionID, sched.issueID, &at); err != nil {
			t.Fatalf("ScheduleProviderLimitRetry(%s/%s): %v", sched.executionID, sched.issueID, err)
		}
	}

	due, err := store.ListDueProviderLimitIssues(ctx, now)
	if err != nil {
		t.Fatalf("ListDueProviderLimitIssues: %v", err)
	}
	got := map[string]string{}
	for _, issue := range due {
		got[issue.ID] = issue.ExecutionID
	}
	want := map[string]string{"issue-due": "exec-a", "issue-other-exec": "exec-b"}
	if len(got) != len(want) {
		t.Fatalf("due issues = %+v, want %+v", got, want)
	}
	for id, executionID := range want {
		if got[id] != executionID {
			t.Fatalf("due issue %s execution = %q, want %q", id, got[id], executionID)
		}
	}
}

// TestListDueProviderLimitIssuesReturnsEmptySliceWhenNoneDue proves the query
// never returns nil, matching ListActiveExecutionLeases.
func TestListDueProviderLimitIssuesReturnsEmptySliceWhenNoneDue(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	due, err := store.ListDueProviderLimitIssues(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("ListDueProviderLimitIssues: %v", err)
	}
	if due == nil {
		t.Fatalf("expected an empty slice, got nil")
	}
	if len(due) != 0 {
		t.Fatalf("expected no due issues, got %+v", due)
	}
}

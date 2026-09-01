package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

func TestClaimExecutionLease(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StateImplementing)

	expiresAt := time.Now().Add(time.Minute)
	if err := store.ClaimExecutionLease(ctx, "exec-1", "issue-1", expiresAt); err != nil {
		t.Fatalf("ClaimExecutionLease: %v", err)
	}

	lease, err := store.ExecutionLease(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("ExecutionLease: %v", err)
	}
	if lease.ExecutionID != "exec-1" || lease.IssueID != "issue-1" {
		t.Fatalf("lease identity mismatch: got %+v", lease)
	}
	if !lease.ExpiresAt.Equal(expiresAt.UTC()) {
		t.Fatalf("expected expiry %v, got %v", expiresAt.UTC(), lease.ExpiresAt)
	}
	if lease.HeartbeatAt.IsZero() || lease.ClaimedAt.IsZero() {
		t.Fatalf("expected heartbeat and claimed timestamps to be set, got %+v", lease)
	}
}

func TestListActiveExecutionLeases(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StateImplementing)
	issueTwo := domain.Issue{
		ID: "issue-2", ExecutionID: "exec-1", State: domain.StateImplementing, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 3, CI: 3}),
	}
	if err := store.CreateIssue(ctx, issueTwo); err != nil {
		t.Fatalf("CreateIssue issue-2: %v", err)
	}

	leaseOneExpiry := time.Now().Add(time.Minute)
	leaseTwoExpiry := time.Now().Add(2 * time.Minute)
	if err := store.ClaimExecutionLease(ctx, "exec-1", "issue-1", leaseOneExpiry); err != nil {
		t.Fatalf("ClaimExecutionLease issue-1: %v", err)
	}
	if err := store.ClaimExecutionLease(ctx, "exec-1", "issue-2", leaseTwoExpiry); err != nil {
		t.Fatalf("ClaimExecutionLease issue-2: %v", err)
	}

	leases, err := store.ListActiveExecutionLeases(ctx)
	if err != nil {
		t.Fatalf("ListActiveExecutionLeases: %v", err)
	}
	if len(leases) != 2 {
		t.Fatalf("expected 2 active leases, got %d: %+v", len(leases), leases)
	}

	byIssue := make(map[string]storage.ExecutionLease, len(leases))
	for _, lease := range leases {
		byIssue[lease.IssueID] = lease
	}
	if lease, ok := byIssue["issue-1"]; !ok || !lease.ExpiresAt.Equal(leaseOneExpiry.UTC()) {
		t.Fatalf("issue-1 lease missing or wrong expiry: %+v", byIssue["issue-1"])
	}
	if lease, ok := byIssue["issue-2"]; !ok || !lease.ExpiresAt.Equal(leaseTwoExpiry.UTC()) {
		t.Fatalf("issue-2 lease missing or wrong expiry: %+v", byIssue["issue-2"])
	}
}

func TestListActiveExecutionLeases_NoneActive_EmptySlice(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	leases, err := store.ListActiveExecutionLeases(ctx)
	if err != nil {
		t.Fatalf("ListActiveExecutionLeases: %v", err)
	}
	if len(leases) != 0 {
		t.Fatalf("expected no active leases, got %+v", leases)
	}
}

func TestClaimExecutionLeaseConflict(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StateImplementing)

	if err := store.ClaimExecutionLease(ctx, "exec-1", "issue-1", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("first claim: %v", err)
	}

	err := store.ClaimExecutionLease(ctx, "exec-1", "issue-1", time.Now().Add(time.Minute))
	if !errors.Is(err, storage.ErrExecutionLeaseHeld) {
		t.Fatalf("expected ErrExecutionLeaseHeld, got %v", err)
	}
	var conflict *storage.ExecutionLeaseConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected *ExecutionLeaseConflictError, got %T: %v", err, err)
	}
	if conflict.ExecutionID != "exec-1" || conflict.IssueID != "issue-1" {
		t.Fatalf("conflict identity mismatch: got %+v", conflict)
	}
}

func TestClaimExecutionLeaseRejectsNonexistentIssue(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{ID: "exec-1", BaseRevision: "base", StartedAt: time.Now()}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	err := store.ClaimExecutionLease(ctx, "exec-1", "issue-ghost", time.Now().Add(time.Minute))
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestExecutionLeaseNotFound(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	if _, err := store.ExecutionLease(ctx, "exec-1", "issue-1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestHeartbeatExecutionLease(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StateImplementing)

	firstExpiry := time.Now().Add(time.Minute)
	if err := store.ClaimExecutionLease(ctx, "exec-1", "issue-1", firstExpiry); err != nil {
		t.Fatalf("ClaimExecutionLease: %v", err)
	}
	claimed, err := store.ExecutionLease(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("ExecutionLease: %v", err)
	}

	renewedExpiry := time.Now().Add(5 * time.Minute)
	if err := store.HeartbeatExecutionLease(ctx, "exec-1", "issue-1", renewedExpiry); err != nil {
		t.Fatalf("HeartbeatExecutionLease: %v", err)
	}

	lease, err := store.ExecutionLease(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("ExecutionLease: %v", err)
	}
	if !lease.ExpiresAt.Equal(renewedExpiry.UTC()) {
		t.Fatalf("expected renewed expiry %v, got %v", renewedExpiry.UTC(), lease.ExpiresAt)
	}
	if lease.HeartbeatAt.Before(claimed.HeartbeatAt) {
		t.Fatalf("expected heartbeat to advance, got %v then %v", claimed.HeartbeatAt, lease.HeartbeatAt)
	}
}

func TestHeartbeatExecutionLeaseNotFound(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	err := store.HeartbeatExecutionLease(ctx, "exec-1", "issue-1", time.Now().Add(time.Minute))
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestReleaseExecutionLease(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StateImplementing)

	if err := store.ClaimExecutionLease(ctx, "exec-1", "issue-1", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("ClaimExecutionLease: %v", err)
	}
	if err := store.ReleaseExecutionLease(ctx, "exec-1", "issue-1"); err != nil {
		t.Fatalf("ReleaseExecutionLease: %v", err)
	}
	if _, err := store.ExecutionLease(ctx, "exec-1", "issue-1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after release, got %v", err)
	}

	// Releasing an already-released (or never-claimed) lease is a no-op.
	if err := store.ReleaseExecutionLease(ctx, "exec-1", "issue-1"); err != nil {
		t.Fatalf("ReleaseExecutionLease (no-op): %v", err)
	}
}

func TestReleaseThenReclaimExecutionLease(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StateImplementing)

	if err := store.ClaimExecutionLease(ctx, "exec-1", "issue-1", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := store.ReleaseExecutionLease(ctx, "exec-1", "issue-1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := store.ClaimExecutionLease(ctx, "exec-1", "issue-1", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("reclaim after release: %v", err)
	}
	if _, err := store.ExecutionLease(ctx, "exec-1", "issue-1"); err != nil {
		t.Fatalf("ExecutionLease after reclaim: %v", err)
	}
}

func TestRecordAndLoadExecutionPlacement(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StateImplementing)

	placement := storage.ExecutionPlacement{
		ExecutionID: "exec-1",
		IssueID:     "issue-1",
		Backend:     "remote",
		WorkerRef:   "worker-a",
		Workspace: domain.Workspace{
			IssueID: "issue-1",
			Path:    "/worker/issue-1",
			Branch:  "forge/issue-1",
		},
		Lifecycle: domain.WorkspaceLifecycleActive,
	}
	if err := store.RecordExecutionPlacement(ctx, placement); err != nil {
		t.Fatalf("RecordExecutionPlacement: %v", err)
	}

	loaded, err := store.ExecutionPlacementByIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("ExecutionPlacementByIssue: %v", err)
	}
	if loaded != placement {
		t.Fatalf("loaded placement mismatch: got %+v, want %+v", loaded, placement)
	}
}

func TestExecutionPlacementByIssueNotFound(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	if _, err := store.ExecutionPlacementByIssue(ctx, "exec-1", "issue-1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestExecutionLeaseLapsed(t *testing.T) {
	expiresAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	lease := storage.ExecutionLease{ExecutionID: "exec-1", IssueID: "issue-1", ExpiresAt: expiresAt}

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"heartbeat-present", expiresAt.Add(-time.Minute), false},
		{"heartbeat-lapsed", expiresAt.Add(time.Minute), true},
		{"heartbeat-lapsed-at-exact-expiry", expiresAt, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lease.Lapsed(tt.now); got != tt.want {
				t.Fatalf("Lapsed(%v) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}
func TestRecordExecutionPlacementReplacesEarlierRecord(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssue(t, store, "exec-1", "issue-1", domain.StateImplementing)

	first := storage.ExecutionPlacement{
		ExecutionID: "exec-1",
		IssueID:     "issue-1",
		Backend:     "remote",
		WorkerRef:   "worker-a",
		Workspace:   domain.Workspace{IssueID: "issue-1", Path: "/worker/a", Branch: "forge/issue-1"},
		Lifecycle:   domain.WorkspaceLifecycleActive,
	}
	if err := store.RecordExecutionPlacement(ctx, first); err != nil {
		t.Fatalf("RecordExecutionPlacement: %v", err)
	}

	second := first
	second.WorkerRef = "worker-b"
	second.Lifecycle = domain.WorkspaceLifecycleLost
	if err := store.RecordExecutionPlacement(ctx, second); err != nil {
		t.Fatalf("RecordExecutionPlacement (replace): %v", err)
	}

	loaded, err := store.ExecutionPlacementByIssue(ctx, "exec-1", "issue-1")
	if err != nil {
		t.Fatalf("ExecutionPlacementByIssue: %v", err)
	}
	if loaded != second {
		t.Fatalf("expected replaced placement %+v, got %+v", second, loaded)
	}
}

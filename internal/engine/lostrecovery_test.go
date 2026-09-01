package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/storage"
)

// seedLostRecoveryFixture creates an Execution, an Issue in issueState with
// a retry budget bounded to gateLimit gate failures, and an active
// ExecutionLease/ExecutionPlacement pair for it, mirroring the lease-store
// fixtures in internal/storage/leases_test.go and the retry-budget setup in
// internal/domain/retry_test.go.
func seedLostRecoveryFixture(t *testing.T, store *storage.SQLiteStore, executionID, issueID string, issueState domain.IssueState, gateLimit int, expiresAt time.Time) {
	t.Helper()
	ctx := context.Background()

	if err := store.CreateExecution(ctx, domain.Execution{ID: executionID, BaseRevision: "base", StartedAt: time.Now()}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	issue := domain.Issue{
		ID: issueID, ExecutionID: executionID, State: issueState, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: gateLimit, Review: 3, CI: 3}),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.ClaimExecutionLease(ctx, executionID, issueID, expiresAt); err != nil {
		t.Fatalf("ClaimExecutionLease: %v", err)
	}
	if err := store.RecordExecutionPlacement(ctx, storage.ExecutionPlacement{
		ExecutionID: executionID,
		IssueID:     issueID,
		Backend:     "remote",
		WorkerRef:   "worker-a",
		Workspace:   domain.Workspace{IssueID: issueID, Path: "/worker/" + issueID, Branch: "forge/" + issueID},
		Lifecycle:   domain.WorkspaceLifecycleActive,
	}); err != nil {
		t.Fatalf("RecordExecutionPlacement: %v", err)
	}
}

// TestRecoverLostExecution is table-driven, mirroring the scenario style of
// internal/storage/leases_test.go and internal/domain/retry_test.go: it
// covers the four cases the LOST-detection + budgeted-retry acceptance
// criteria calls out — heartbeat-present, heartbeat-lapsed (which retries
// within budget), and budget-exhausted (terminal).
func TestRecoverLostExecution(t *testing.T) {
	tests := []struct {
		name          string
		gateLimit     int
		nowAfterLease func(expiresAt time.Time) time.Time

		wantLost    bool
		wantRetried bool
		wantErr     bool
	}{
		{
			name:          "heartbeat-present",
			gateLimit:     3,
			nowAfterLease: func(expiresAt time.Time) time.Time { return expiresAt.Add(-time.Second) },
			wantLost:      false,
			wantRetried:   false,
			wantErr:       false,
		},
		{
			name:          "heartbeat-lapsed-retries-within-budget",
			gateLimit:     3,
			nowAfterLease: func(expiresAt time.Time) time.Time { return expiresAt.Add(time.Second) },
			wantLost:      true,
			wantRetried:   true,
			wantErr:       false,
		},
		{
			name:          "heartbeat-lapsed-budget-exhausted",
			gateLimit:     0,
			nowAfterLease: func(expiresAt time.Time) time.Time { return expiresAt.Add(time.Second) },
			wantLost:      true,
			wantRetried:   false,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openTestStore(t)
			ctx := context.Background()
			expiresAt := time.Now().Add(time.Minute)
			seedLostRecoveryFixture(t, store, "exec-1", "issue-1", domain.StateImplementing, tt.gateLimit, expiresAt)

			now := tt.nowAfterLease(expiresAt)
			result, err := engine.RecoverLostExecution(ctx, store, "exec-1", "issue-1", func() time.Time { return now })

			if tt.wantErr {
				var exhausted *domain.RetryExhaustedError
				if !errors.As(err, &exhausted) {
					t.Fatalf("expected *domain.RetryExhaustedError, got %T: %v", err, err)
				}
			} else if err != nil {
				t.Fatalf("RecoverLostExecution: %v", err)
			}
			if result.Lost != tt.wantLost {
				t.Fatalf("Lost = %v, want %v", result.Lost, tt.wantLost)
			}
			if result.Retried != tt.wantRetried {
				t.Fatalf("Retried = %v, want %v", result.Retried, tt.wantRetried)
			}

			// The Issue always stays in its existing IssueState, whichever
			// branch ran.
			reloaded, err := store.GetIssue(ctx, "exec-1", "issue-1")
			if err != nil {
				t.Fatalf("GetIssue: %v", err)
			}
			if reloaded.State != domain.StateImplementing {
				t.Fatalf("Issue.State = %s, want unchanged IMPLEMENTING", reloaded.State)
			}

			if !tt.wantLost {
				// heartbeat-present: nothing was touched.
				if _, err := store.ExecutionLease(ctx, "exec-1", "issue-1"); err != nil {
					t.Fatalf("expected lease to remain held, got %v", err)
				}
				placement, err := store.ExecutionPlacementByIssue(ctx, "exec-1", "issue-1")
				if err != nil {
					t.Fatalf("ExecutionPlacementByIssue: %v", err)
				}
				if placement.Lifecycle != domain.WorkspaceLifecycleActive {
					t.Fatalf("Lifecycle = %s, want ACTIVE", placement.Lifecycle)
				}
				return
			}

			// Lost, whether retried or budget-exhausted: the lease is
			// expired and the Workspace is non-authoritative either way.
			if _, err := store.ExecutionLease(ctx, "exec-1", "issue-1"); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("expected lease to be expired, got %v", err)
			}
			placement, err := store.ExecutionPlacementByIssue(ctx, "exec-1", "issue-1")
			if err != nil {
				t.Fatalf("ExecutionPlacementByIssue: %v", err)
			}
			if placement.Lifecycle != domain.WorkspaceLifecycleLost {
				t.Fatalf("Lifecycle = %s, want LOST", placement.Lifecycle)
			}

			wantGateFailures := 0
			if tt.wantRetried {
				wantGateFailures = 1
			}
			if reloaded.RetryBudget.GateFailures() != wantGateFailures {
				t.Fatalf("persisted GateFailures = %d, want %d", reloaded.RetryBudget.GateFailures(), wantGateFailures)
			}
		})
	}
}

func TestRecoverLostExecution_NoActiveLease_NoOp(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	seedExecutionAndIssueForLostRecovery(t, store, "exec-1", "issue-1", domain.StateImplementing)

	result, err := engine.RecoverLostExecution(ctx, store, "exec-1", "issue-1", time.Now)
	if err != nil {
		t.Fatalf("RecoverLostExecution: %v", err)
	}
	if result.Lost || result.Retried {
		t.Fatalf("result = %+v, want zero-value when no lease is held", result)
	}
}

func seedExecutionAndIssueForLostRecovery(t *testing.T, store *storage.SQLiteStore, executionID, issueID string, state domain.IssueState) {
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

package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
)

// TestUpdateIssueStateCASRejectsStaleFrom is a white-box test of the
// compare-and-swap guard TransitionIssue relies on: an UPDATE whose WHERE
// clause pins the previously-read state must affect zero rows once that
// state no longer matches, rather than clobbering whatever is actually
// there.
func TestUpdateIssueStateCASRejectsStaleFrom(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "forge.db")
	store, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := store.CreateExecution(ctx, domain.Execution{ID: "exec-1", BaseRevision: "base"}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	issue := domain.Issue{
		ID: "issue-1", ExecutionID: "exec-1", State: domain.StateReady, Scope: domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(domain.RetryLimits{Gate: 1, Review: 1, CI: 1}),
	}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	// The row is actually READY; assert a stale "from" (PENDING) affects no
	// rows instead of forcing the write through.
	affected, err := updateIssueStateCAS(ctx, tx, "exec-1", "issue-1", string(domain.StatePending), string(domain.StateClaimed), time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("updateIssueStateCAS with stale from: %v", err)
	}
	if affected != 0 {
		t.Fatalf("expected 0 rows affected for a stale from-state, got %d", affected)
	}

	// The correct "from" (READY) affects exactly one row.
	affected, err = updateIssueStateCAS(ctx, tx, "exec-1", "issue-1", string(domain.StateReady), string(domain.StateClaimed), time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("updateIssueStateCAS with correct from: %v", err)
	}
	if affected != 1 {
		t.Fatalf("expected 1 row affected for the correct from-state, got %d", affected)
	}
}

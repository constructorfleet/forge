package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/storage"
)

// conflictingCancelStore reports one fixed cancel-claim conflict, mirroring
// conflictingClaimStore for ClaimRetry: it drives the CAS-miss path
// deterministically instead of racing a second goroutine against the real
// store's single connection.
type conflictingCancelStore struct {
	storage.Store
	state domain.IssueState
}

func (s conflictingCancelStore) ClaimCancel(_ context.Context, executionID, issueID string, _ domain.IssueState, _ bool) (domain.Issue, error) {
	return domain.Issue{}, &storage.CancelClaimConflictError{ExecutionID: executionID, IssueID: issueID, State: s.state}
}

// TestCancelExecution_AlreadyCancelledConflictIsANoOp proves that when
// ClaimCancel's compare-and-set loses because another actor already
// finished cancelling the Issue, CancelExecution treats it as done rather
// than failing the whole cancel.
func TestCancelExecution_AlreadyCancelledConflictIsANoOp(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"554": {ID: "554", Title: "Already cancelled by someone else"},
	})
	ctx := context.Background()
	exec := domain.Execution{ID: "exec-554a", BaseRevision: te.base}
	if err := te.store.CreateExecution(ctx, exec); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if err := te.store.CreateIssue(ctx, domain.Issue{
		ID: "554", ExecutionID: exec.ID, Title: "Already cancelled by someone else",
		State: domain.StateImplementing, Scope: domain.ScopeManaged,
	}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	te.eng.Store = conflictingCancelStore{Store: te.store, state: domain.StateCancelled}

	if _, err := te.eng.CancelExecution(ctx, exec.ID); err != nil {
		t.Fatalf("CancelExecution: %v, want the already-cancelled conflict treated as a no-op", err)
	}
}

// TestCancelExecution_ReportsAnotherActorMovedTheIssue proves that when
// ClaimCancel's compare-and-set loses to a non-terminal state (something
// else moved the Issue on, not cancelled it), CancelExecution surfaces
// that instead of silently swallowing it (issue 554).
func TestCancelExecution_ReportsAnotherActorMovedTheIssue(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"555": {ID: "555", Title: "Moved by another actor"},
	})
	ctx := context.Background()
	exec := domain.Execution{ID: "exec-554b", BaseRevision: te.base}
	if err := te.store.CreateExecution(ctx, exec); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if err := te.store.CreateIssue(ctx, domain.Issue{
		ID: "555", ExecutionID: exec.ID, Title: "Moved by another actor",
		State: domain.StateImplementing, Scope: domain.ScopeManaged,
	}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	te.eng.Store = conflictingCancelStore{Store: te.store, state: domain.StateValidating}

	_, err := te.eng.CancelExecution(ctx, exec.ID)
	if err == nil {
		t.Fatal("CancelExecution error = nil, want a report naming the actor that moved the Issue")
	}
	if !strings.Contains(err.Error(), "555") || !strings.Contains(err.Error(), string(domain.StateValidating)) {
		t.Fatalf("CancelExecution error = %v, want it to name issue 555 and VALIDATING", err)
	}
	var ownerErr *engine.CancelOwnerError
	if errors.As(err, &ownerErr) {
		t.Fatalf("CancelExecution error = %v, want a plain cancel-conflict report, not CancelOwnerError", err)
	}
}

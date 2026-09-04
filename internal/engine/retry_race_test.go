package engine_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/storage"
)

// conflictingClaimStore reports one fixed retry-claim conflict, which is the
// only way to drive a lost claim deterministically: the real store
// serializes its claims on one connection.
type conflictingClaimStore struct {
	storage.Store
	state domain.IssueState
}

func (s conflictingClaimStore) ClaimRetry(_ context.Context, executionID, issueID string, _ domain.RetryBudget) (storage.RetryClaim, error) {
	return storage.RetryClaim{}, &storage.RetryClaimConflictError{ExecutionID: executionID, IssueID: issueID, State: s.state}
}

// runToFailed executes issueID once with a failing Agent and returns the
// Execution ID of the resulting FAILED Issue.
func runToFailed(t *testing.T, te testEngine, issueID string) string {
	t.Helper()
	te.fake.ProgramResult(issueID, agent.AgentResult{Status: agent.StatusFailed, Summary: "boom"})
	te.fake.ProgramDefault(agent.AgentResult{Status: agent.StatusImplemented, Summary: "fixed"})

	result, err := te.eng.Execute(context.Background(), issueID, te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("initial state = %s, want FAILED", result.Issue.State)
	}
	return result.ExecutionID
}

// TestRetryIssue_LoserOfRaceTouchesNothing drives the race: another actor
// claims the retry first. The loser must report ErrRetryAlreadyClaimed and
// must not rebase the shared Workspace under the winner.
func TestRetryIssue_LoserOfRaceTouchesNothing(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"456": {ID: "456", Title: "Racing retries"},
	})
	executionID := runToFailed(t, te, "456")
	ctx := context.Background()

	newTip := advanceTarget(t, te.eng.RepoRoot, "unrelated advance")
	te.eng.TargetTip = engine.TargetTipResolverFunc(func(context.Context) (string, error) {
		return newTip, nil
	})
	te.eng.Ancestry = fakeAncestorChecker{ok: true}
	te.eng.Store = conflictingClaimStore{Store: te.store, state: domain.StateReady}

	_, err := te.eng.RetryIssue(ctx, executionID, "456")
	if !errors.Is(err, engine.ErrRetryAlreadyClaimed) {
		t.Fatalf("RetryIssue error = %v, want ErrRetryAlreadyClaimed", err)
	}
	if errors.Is(err, storage.ErrConcurrentModification) {
		t.Fatalf("RetryIssue leaked the raw store error: %v", err)
	}
	if te.ws.RebaseCalled() {
		t.Fatal("the losing retry rebased the Workspace the winner now owns")
	}
}

// TestRetryIssue_ReportsAConcurrentCancelAsItself proves a state the claim
// lost to for another reason is not reported as a rival retry.
func TestRetryIssue_ReportsAConcurrentCancelAsItself(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"458": {ID: "458", Title: "Cancelled mid-retry"},
	})
	executionID := runToFailed(t, te, "458")
	te.eng.Store = conflictingClaimStore{Store: te.store, state: domain.StateCancelled}

	_, err := te.eng.RetryIssue(context.Background(), executionID, "458")
	if errors.Is(err, engine.ErrRetryAlreadyClaimed) {
		t.Fatalf("RetryIssue error = %v, want a cancel report, not ErrRetryAlreadyClaimed", err)
	}
	if err == nil || !strings.Contains(err.Error(), string(domain.StateCancelled)) {
		t.Fatalf("RetryIssue error = %v, want it to name CANCELLED", err)
	}
}

// TestRetryIssue_ConcurrentRetriesElectOneWinner proves two simultaneous
// retries of the same Issue never both run it: one succeeds and the other
// reports a clear reason.
func TestRetryIssue_ConcurrentRetriesElectOneWinner(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"457": {ID: "457", Title: "Two retries at once"},
	})
	executionID := runToFailed(t, te, "457")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = te.eng.RetryIssue(context.Background(), executionID, "457")
		}(i)
	}
	wg.Wait()

	var wins int
	for _, err := range errs {
		if err == nil {
			wins++
			continue
		}
		if errors.Is(err, storage.ErrConcurrentModification) {
			t.Fatalf("loser leaked the raw store error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("successful retries = %d, want exactly 1 (errors=%v)", wins, errs)
	}
}

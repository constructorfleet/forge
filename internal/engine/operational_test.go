package engine_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gate"
	"github.com/Teagan42/forge/internal/storage"
)

type blockingAgent struct {
	started chan struct{}
	once    sync.Once
}

func newBlockingAgent() *blockingAgent {
	return &blockingAgent{started: make(chan struct{})}
}

func (a *blockingAgent) Execute(ctx context.Context, _ agent.AgentRequest) (agent.AgentResult, error) {
	a.once.Do(func() { close(a.started) })
	<-ctx.Done()
	return agent.AgentResult{Status: agent.StatusFailed, Summary: "cancelled"}, ctx.Err()
}

func TestRetryIssue_RerunsFailedIssue(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"61": {ID: "61", Title: "Retry me"},
	})
	te.fake.ProgramResult("61", agent.AgentResult{Status: agent.StatusFailed, Summary: "boom"})
	te.fake.ProgramResult("61", agent.AgentResult{Status: agent.StatusImplemented, Summary: "fixed"})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "61", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("initial state = %s, want FAILED", result.Issue.State)
	}

	retried, err := te.eng.RetryIssue(ctx, result.ExecutionID, "61")
	if err != nil {
		t.Fatalf("RetryIssue: %v", err)
	}
	if retried.State != domain.StateCommitting {
		t.Fatalf("retried state = %s, want COMMITTING", retried.State)
	}

	issue, err := te.store.GetIssue(ctx, result.ExecutionID, "61")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateCommitting {
		t.Fatalf("persisted state = %s, want COMMITTING", issue.State)
	}
}

// TestRetryIssue_RejectsNonFailedIssue covers READY as well as a live state.
// READY is also the state a rival retry's winner leaves, so a queued Issue
// that never failed must still get the plain non-FAILED error, not the
// no-op "another actor already claimed this retry" report.
func TestRetryIssue_RejectsNonFailedIssue(t *testing.T) {
	for _, state := range []domain.IssueState{domain.StateClaimed, domain.StateReady} {
		t.Run(string(state), func(t *testing.T) {
			te := newTestEngine(t, map[string]domain.Issue{
				"62": {ID: "62", Title: "Still running"},
			})
			ctx := context.Background()
			exec := domain.Execution{ID: "exec-62", BaseRevision: te.base, StartedAt: time.Now().UTC()}
			if err := te.store.CreateExecution(ctx, exec); err != nil {
				t.Fatalf("CreateExecution: %v", err)
			}
			if err := te.store.CreateIssue(ctx, domain.Issue{
				ID:          "62",
				ExecutionID: exec.ID,
				Title:       "Still running",
				State:       state,
				Scope:       domain.ScopeManaged,
				RetryBudget: domain.NewRetryBudget(te.eng.Config.Retry),
			}); err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}

			_, err := te.eng.RetryIssue(ctx, exec.ID, "62")
			want := fmt.Sprintf("engine: retry issue 62: issue is %s, want FAILED", state)
			if err == nil || err.Error() != want {
				t.Fatalf("RetryIssue error = %v, want %q", err, want)
			}
			if errors.Is(err, engine.ErrRetryAlreadyClaimed) {
				t.Fatalf("RetryIssue error = %v, want a non-FAILED report, not a no-op", err)
			}
		})
	}
}

type failThenPassRunner struct {
	mu        sync.Mutex
	calls     int
	failUntil int
}

func (r *failThenPassRunner) Run(context.Context, string, string, io.Writer, io.Writer) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.calls <= r.failUntil {
		return 1, nil
	}
	return 0, nil
}

var _ gate.CommandRunner = (*failThenPassRunner)(nil)

func TestRetryIssue_ResetsExhaustedRetryBudget(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"64": {ID: "64", Title: "Budget reset"},
	})
	te.fake.ProgramDefault(agent.AgentResult{Status: agent.StatusImplemented, Summary: "implemented"})
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}
	te.eng.Config.Retry = domain.RetryLimits{Gate: 1, Review: 1, CI: 1}
	te.gates.Set(&failThenPassRunner{failUntil: 2})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "64", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("initial state = %s, want FAILED", result.Issue.State)
	}

	issue, err := te.store.GetIssue(ctx, result.ExecutionID, "64")
	if err != nil {
		t.Fatalf("GetIssue before retry: %v", err)
	}
	if issue.RetryBudget.GateFailures() != 1 {
		t.Fatalf("GateFailures before retry = %d, want 1", issue.RetryBudget.GateFailures())
	}

	retried, err := te.eng.RetryIssue(ctx, result.ExecutionID, "64")
	if err != nil {
		t.Fatalf("RetryIssue: %v", err)
	}
	if retried.State != domain.StateCommitting {
		t.Fatalf("retried state = %s, want COMMITTING", retried.State)
	}

	issue, err = te.store.GetIssue(ctx, result.ExecutionID, "64")
	if err != nil {
		t.Fatalf("GetIssue after retry: %v", err)
	}
	if issue.RetryBudget.GateFailures() != 0 {
		t.Fatalf("GateFailures after retry = %d, want 0", issue.RetryBudget.GateFailures())
	}
}

func TestCancelExecution_StatusShowsCancelledWithoutWorkspaceCorruption(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"63": {ID: "63", Title: "Cancel me"},
	})
	blocker := newBlockingAgent()
	te.eng.Agent = blocker
	te.eng.OwnerPID = func() int { return 4242 }

	ctx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	exec, err := te.eng.StartExecution(context.Background(), te.base)
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := te.eng.ExecuteInExecution(ctx, exec, "63", te.base)
		done <- err
	}()

	select {
	case <-blocker.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for agent execution to start")
	}

	before, err := engine.LoadStatus(context.Background(), te.store, exec.ID)
	if err != nil {
		t.Fatalf("LoadStatus before cancel: %v", err)
	}
	if len(before.Issues) != 1 || before.Issues[0].Issue.State != domain.StateImplementing {
		t.Fatalf("pre-cancel status = %+v, want one IMPLEMENTING issue", before.Issues)
	}

	controller := engine.New(te.store, te.trk, te.ws, te.eng.Agent, te.eng.Config, te.eng.RepoRoot)
	controller.ProcessRunning = func(pid int) (bool, error) { return pid == 4242, nil }
	controller.InterruptProcess = func(pid int) error {
		cancelRun()
		controller.ProcessRunning = func(int) (bool, error) { return false, nil }
		return nil
	}
	controller.WaitForProcessExit = func(context.Context, int) error { return nil }

	cancelled, err := controller.CancelExecution(context.Background(), exec.ID)
	if err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}
	if len(cancelled.Issues) != 1 || cancelled.Issues[0].State != domain.StateCancelled {
		t.Fatalf("cancelled issues = %+v, want one CANCELLED issue", cancelled.Issues)
	}

	runErr := <-done
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("execute error = %v, want context.Canceled", runErr)
	}

	after, err := engine.LoadStatus(context.Background(), te.store, exec.ID)
	if err != nil {
		t.Fatalf("LoadStatus after cancel: %v", err)
	}
	if len(after.Issues) != 1 || after.Issues[0].Issue.State != domain.StateCancelled {
		t.Fatalf("post-cancel status = %+v, want one CANCELLED issue", after.Issues)
	}

	ws, err := te.store.WorkspaceByIssue(context.Background(), exec.ID, "63")
	if err != nil {
		t.Fatalf("WorkspaceByIssue: %v", err)
	}
	if ws.Path == "" {
		t.Fatal("workspace path empty after cancel")
	}
	if _, err := te.store.WorkerClaim(context.Background(), exec.ID, "63"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("WorkerClaim after cancel = %v, want ErrNotFound", err)
	}
}

// A stale owner_pid that the operating system reused points at an unrelated
// process. Cancel must not signal it, and must still cancel the Issues.
func TestCancelExecution_RecycledOwnerPIDIsNotSignalled(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{"65": {ID: "65"}})
	te.eng.OwnerPID = func() int { return 111 }
	te.eng.ProcessRunning = func(int) (bool, error) { return true, nil }
	te.eng.ProcessStartToken = func(context.Context, int) string { return "start-recycled" }
	var signalled []int
	te.eng.InterruptProcess = func(pid int) error {
		signalled = append(signalled, pid)
		return nil
	}

	executionID, _ := seedRecoveryExecution(t, te, domain.Issue{ID: "65", Title: "Cancel recycled owner"}, domain.StateImplementing, 222)

	cancelled, err := te.eng.CancelExecution(context.Background(), executionID)
	if err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}
	if len(signalled) != 0 {
		t.Fatalf("signalled pids = %v, want none", signalled)
	}
	if len(cancelled.Issues) != 1 || cancelled.Issues[0].State != domain.StateCancelled {
		t.Fatalf("cancelled issues = %+v, want one CANCELLED issue", cancelled.Issues)
	}
}

// An owner the Engine cannot inspect is neither signalled nor released.
// Signalling could hit an unrelated process that reused the pid, and
// releasing the claim could hand the Issue to a second Execution while the
// original owner still writes to the Workspace.
func TestCancelExecution_UninspectableOwnerKeepsItsClaim(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{"67": {ID: "67"}})
	te.eng.OwnerPID = func() int { return 111 }
	inspectErr := errors.New("cannot inspect process 222")
	te.eng.ProcessRunning = func(int) (bool, error) { return false, inspectErr }
	var signalled []int
	te.eng.InterruptProcess = func(pid int) error {
		signalled = append(signalled, pid)
		return nil
	}

	executionID, _ := seedRecoveryExecution(t, te, domain.Issue{ID: "67", Title: "Cancel uninspectable owner"}, domain.StateImplementing, 222)

	cancelled, err := te.eng.CancelExecution(context.Background(), executionID)
	var ownerErr *engine.CancelOwnerError
	if !errors.As(err, &ownerErr) {
		t.Fatalf("CancelExecution error = %v, want *engine.CancelOwnerError", err)
	}
	if len(signalled) != 0 {
		t.Fatalf("signalled pids = %v, want none: the pid may belong to another process", signalled)
	}
	if len(cancelled.Issues) != 1 || cancelled.Issues[0].State != domain.StateCancelled {
		t.Fatalf("cancelled issues = %+v, want one CANCELLED issue", cancelled.Issues)
	}
	if _, err := te.store.WorkerClaim(context.Background(), executionID, "67"); err != nil {
		t.Fatalf("WorkerClaim after cancel = %v, want the claim kept", err)
	}
}

// A worker that survives the interrupt makes cancel report an error, but the
// Issues must still reach CANCELLED so the Execution is not left in limbo.
func TestCancelExecution_SurvivingOwnerStillCancelsIssues(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{"66": {ID: "66"}})
	te.eng.OwnerPID = func() int { return 111 }
	te.eng.ProcessRunning = func(int) (bool, error) { return true, nil }
	te.eng.ProcessStartToken = func(_ context.Context, pid int) string { return ownerToken(pid) }
	te.eng.InterruptProcess = func(int) error { return nil }
	waitErr := errors.New("process 222 still running after cancellation timeout")
	te.eng.WaitForProcessExit = func(context.Context, int) error { return waitErr }

	executionID, _ := seedRecoveryExecution(t, te, domain.Issue{ID: "66", Title: "Cancel surviving owner"}, domain.StateImplementing, 222)

	cancelled, err := te.eng.CancelExecution(context.Background(), executionID)
	if !errors.Is(err, waitErr) {
		t.Fatalf("CancelExecution error = %v, want %v", err, waitErr)
	}
	// The error reports only the owner, so a caller can keep the state.
	var ownerErr *engine.CancelOwnerError
	if !errors.As(err, &ownerErr) {
		t.Fatalf("CancelExecution error = %T, want *engine.CancelOwnerError", err)
	}
	if cancelled.Execution.ID != executionID {
		t.Fatalf("cancelled.Execution.ID = %q, want %q", cancelled.Execution.ID, executionID)
	}

	issue, err := te.store.GetIssue(context.Background(), executionID, "66")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateCancelled {
		t.Fatalf("state after cancel = %s, want CANCELLED", issue.State)
	}

	// The old owner still runs, so a second Execution must not be able to
	// claim the same Issue.
	claim, err := te.store.WorkerClaim(context.Background(), executionID, "66")
	if err != nil {
		t.Fatalf("WorkerClaim after cancel with a surviving owner: %v", err)
	}
	if claim.OwnerPID != 222 {
		t.Fatalf("claim.OwnerPID = %d, want 222", claim.OwnerPID)
	}

	events, err := te.store.EventsByExecution(context.Background(), executionID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	var data string
	for _, ev := range events {
		if ev.Type == "execution.cancelled" {
			data = ev.Data
		}
	}
	if !strings.Contains(data, `"unstopped_owners":"66:222"`) {
		t.Fatalf("execution.cancelled data = %q, want it to name owner 66:222", data)
	}
}

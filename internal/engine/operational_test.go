package engine_test

import (
	"context"
	"errors"
	"io"
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

func TestRetryIssue_RejectsNonFailedIssue(t *testing.T) {
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
		State:       domain.StateReady,
		Scope:       domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(te.eng.Config.Retry),
	}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	_, err := te.eng.RetryIssue(ctx, exec.ID, "62")
	if err == nil || err.Error() != "engine: issue 62 is READY, want FAILED" {
		t.Fatalf("RetryIssue error = %v, want clear non-FAILED error", err)
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
	te.eng.Gates = &failThenPassRunner{failUntil: 2}

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

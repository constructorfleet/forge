package engine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/storage"
)

// capturingDeadlineAgent records the ctx passed to Execute so a test can
// inspect the deadline executeAgent derived for it, then returns result
// immediately without blocking.
type capturingDeadlineAgent struct {
	result     agent.AgentResult
	capturedAt chan context.Context
}

func newCapturingDeadlineAgent(result agent.AgentResult) *capturingDeadlineAgent {
	return &capturingDeadlineAgent{result: result, capturedAt: make(chan context.Context, 1)}
}

func (a *capturingDeadlineAgent) Execute(ctx context.Context, _ agent.AgentRequest) (agent.AgentResult, error) {
	a.capturedAt <- ctx
	return a.result, nil
}

var _ agent.Agent = (*capturingDeadlineAgent)(nil)

// blockingUntilCanceledAgent blocks until ctx is Done, then returns ctx's
// error — it stands in for a non-adapter hang (e.g. workspace setup) that
// #455's per-adapter idle timeout cannot see, since that hang never reaches
// an adapter's own Execute loop.
type blockingUntilCanceledAgent struct{}

func (blockingUntilCanceledAgent) Execute(ctx context.Context, _ agent.AgentRequest) (agent.AgentResult, error) {
	<-ctx.Done()
	return agent.AgentResult{}, ctx.Err()
}

var _ agent.Agent = blockingUntilCanceledAgent{}

func newDeadlineTestEngine(t *testing.T, timeout time.Duration) (*Engine, *storage.SQLiteStore, domain.Execution, domain.Issue) {
	t.Helper()
	store := openAgentInvocationTestStore(t)
	ctx := context.Background()

	eng := &Engine{
		Store:          store,
		Agent:          poisonAgent{t: t},
		Now:            time.Now,
		NewExecutionID: func() string { return "exec-1" },
		Config:         config.Config{Agent: config.AgentConfig{Timeout: timeout}},
	}

	execRow, err := eng.StartExecution(ctx, "base-sha")
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}
	issue := domain.Issue{ID: "42", ExecutionID: execRow.ID, State: domain.StatePreparing}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	return eng, store, execRow, issue
}

// TestExecuteAgent_AppliesDeadlineAsMultipleOfAgentTimeout proves executeAgent
// (constructorfleet/forge#467) derives its own deadline for env.Agent().Execute
// from Config.Agent.Timeout, strictly greater than Timeout itself so it never
// pre-empts an adapter's own idle timeout (constructorfleet/forge#455).
func TestExecuteAgent_AppliesDeadlineAsMultipleOfAgentTimeout(t *testing.T) {
	const timeout = 2 * time.Minute
	eng, _, execRow, issue := newDeadlineTestEngine(t, timeout)

	envAgent := newCapturingDeadlineAgent(agent.AgentResult{Status: agent.StatusImplemented, Summary: "done"})
	env := execution.NewFakeEnvironmentWithAgent(
		domain.Workspace{IssueID: "42", Path: filepath.Join(t.TempDir(), "ws")},
		envAgent,
	)

	before := time.Now()
	repoCtx := agent.RepositoryContext{BaseRevision: "base-sha"}
	if _, implemented, err := eng.continueAgent(context.Background(), execRow.ID, "42", env, repoCtx, issue, nil); err != nil {
		t.Fatalf("continueAgent: %v", err)
	} else if !implemented {
		t.Fatal("continueAgent: implemented = false, want true")
	}
	after := time.Now()

	var capturedCtx context.Context
	select {
	case capturedCtx = <-envAgent.capturedAt:
	default:
		t.Fatal("Execute was never invoked")
	}

	deadline, ok := capturedCtx.Deadline()
	if !ok {
		t.Fatal("ctx passed to Execute has no deadline, want one derived from Config.Agent.Timeout")
	}

	minDeadline := before.Add(timeout)
	if !deadline.After(minDeadline) {
		t.Errorf("deadline = %v, want strictly after started+Timeout (%v) so it never pre-empts the adapter's own idle timeout", deadline, minDeadline)
	}

	maxDeadline := after.Add(10 * timeout)
	if deadline.After(maxDeadline) {
		t.Errorf("deadline = %v, want within a bounded multiple of Timeout (<= %v)", deadline, maxDeadline)
	}
}

// TestExecuteAgent_DeadlineBoundsAHangOutsideTheAdapter proves the engine's
// own deadline cancels a run that hangs before ever reaching an adapter's
// own liveness timeout — the gap #455 left open, since a hang outside
// Execute's subprocess loop never resets an adapter-owned deadline.
func TestExecuteAgent_DeadlineBoundsAHangOutsideTheAdapter(t *testing.T) {
	const timeout = 20 * time.Millisecond
	eng, _, execRow, issue := newDeadlineTestEngine(t, timeout)

	env := execution.NewFakeEnvironmentWithAgent(
		domain.Workspace{IssueID: "42", Path: filepath.Join(t.TempDir(), "ws")},
		blockingUntilCanceledAgent{},
	)

	repoCtx := agent.RepositoryContext{BaseRevision: "base-sha"}
	done := make(chan error, 1)
	go func() {
		_, _, err := eng.continueAgent(context.Background(), execRow.ID, "42", env, repoCtx, issue, nil)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("continueAgent: err = nil, want an error from the engine's own deadline firing")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("continueAgent: err = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("continueAgent never returned; engine's own deadline did not bound the hang")
	}
}

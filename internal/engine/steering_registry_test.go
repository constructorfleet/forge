package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/steering"
)

// registerDuringFirstCallAgent wraps an agent.Agent and, on its first
// Execute call only, steers loopID through registry before delegating to
// inner — a stand-in for `forge steer` reaching a loop-id while that loop's
// Queue is registered and its first step is still in flight.
type registerDuringFirstCallAgent struct {
	inner    agent.Agent
	registry *steering.Registry
	loopID   string
	steerErr error
	calls    int
}

func (a *registerDuringFirstCallAgent) Execute(ctx context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	a.calls++
	if a.calls == 1 {
		a.steerErr = a.registry.Steer(a.loopID, "steer this loop")
	}
	return a.inner.Execute(ctx, req)
}

var _ agent.Agent = (*registerDuringFirstCallAgent)(nil)

// TestExecuteInExecution_RegistersSteeringQueueUnderExecutionID proves that
// a running execute loop's Queue is reachable through its SteeringRegistry
// by loop-id (the Execution's ID) while the loop is still running — the gap
// constructorfleet/forge#746 closes: until this wiring, `forge steer`
// always resolved loop-id lookups against an empty registry.
func TestExecuteInExecution_RegistersSteeringQueueUnderExecutionID(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"60": {ID: "60"},
	})
	te.fake.ProgramResult("60", agent.AgentResult{Status: agent.StatusImplemented})

	const executionID = "exec-steering-746"
	te.eng.NewExecutionID = func() string { return executionID }

	registry := steering.NewRegistry()
	te.eng.SteeringRegistry = registry
	te.eng.Steering = steering.NewQueue()
	te.eng.SteeringFactory = func() *steering.Queue { return steering.NewQueue() }

	wrapped := &registerDuringFirstCallAgent{inner: te.eng.Agent, registry: registry, loopID: executionID}
	te.eng.Agent = wrapped

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "60", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExecutionID != executionID {
		t.Fatalf("ExecutionID = %q, want %q", result.ExecutionID, executionID)
	}
	if wrapped.calls == 0 {
		t.Fatal("wrapped agent was never called")
	}
	if wrapped.steerErr != nil {
		t.Fatalf("registry.Steer(loopID, ...) during the loop's first step = %v, want nil (loop-id registered)", wrapped.steerErr)
	}

	if err := registry.Steer(executionID, "too late"); !errors.Is(err, steering.ErrLoopNotFound) {
		t.Fatalf("registry.Steer(loopID, ...) after Execute returned = %v, want ErrLoopNotFound (loop-id unregistered once the loop finished)", err)
	}
}

// issueGatedAgent wraps an agent.Agent and, only for issueID, blocks until
// release is closed before delegating to inner — letting a test hold one
// concurrent Worker's ExecuteInExecution call in flight while it inspects
// registry state.
type issueGatedAgent struct {
	inner   agent.Agent
	issueID string
	started chan struct{}
	release chan struct{}
}

func (a *issueGatedAgent) Execute(ctx context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	if req.Issue.ID == a.issueID {
		close(a.started)
		<-a.release
	}
	return a.inner.Execute(ctx, req)
}

var _ agent.Agent = (*issueGatedAgent)(nil)

// TestExecuteInExecution_ConcurrentWorkersShareLoopIDUntilBothFinish proves
// the fix for constructorfleet/forge#746's concurrent-Workers gap:
// internal/scheduler dispatches ExecuteInExecution concurrently, one
// goroutine per ready Issue, and every Issue in one Run shares the same
// Execution and so the same loop-id. One Worker finishing must not
// unregister the loop-id while a sibling Worker under the same Execution is
// still running.
func TestExecuteInExecution_ConcurrentWorkersShareLoopIDUntilBothFinish(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"70": {ID: "70"},
		"71": {ID: "71"},
	})
	te.fake.ProgramResult("70", agent.AgentResult{Status: agent.StatusImplemented})
	te.fake.ProgramResult("71", agent.AgentResult{Status: agent.StatusImplemented})

	registry := steering.NewRegistry()
	te.eng.SteeringRegistry = registry
	te.eng.Steering = steering.NewQueue()
	te.eng.SteeringFactory = func() *steering.Queue { return steering.NewQueue() }

	blocker := &issueGatedAgent{inner: te.eng.Agent, issueID: "71", started: make(chan struct{}), release: make(chan struct{})}
	te.eng.Agent = blocker

	execution := domain.Execution{ID: "exec-shared-746", BaseRevision: te.base, StartedAt: time.Unix(1710000000, 0).UTC()}
	if err := te.store.CreateExecution(context.Background(), execution); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	done70 := make(chan error, 1)
	go func() {
		_, err := te.eng.ExecuteInExecution(context.Background(), execution, "70", te.base)
		done70 <- err
	}()

	done71 := make(chan error, 1)
	go func() {
		_, err := te.eng.ExecuteInExecution(context.Background(), execution, "71", te.base)
		done71 <- err
	}()

	<-blocker.started
	if err := <-done70; err != nil {
		t.Fatalf("ExecuteInExecution(70): %v", err)
	}

	// Issue 70's Worker finished and unregistered, but Issue 71's Worker
	// (same execution.ID) is still blocked mid-step, so the loop-id must
	// still resolve.
	if err := registry.Steer(execution.ID, "still running"); err != nil {
		t.Fatalf("registry.Steer(loopID, ...) while sibling Worker in flight = %v, want nil", err)
	}

	close(blocker.release)
	if err := <-done71; err != nil {
		t.Fatalf("ExecuteInExecution(71): %v", err)
	}

	if err := registry.Steer(execution.ID, "too late"); !errors.Is(err, steering.ErrLoopNotFound) {
		t.Fatalf("registry.Steer(loopID, ...) after both Workers finished = %v, want ErrLoopNotFound", err)
	}
}

// TestExecuteInExecution_NoSteeringQueueRegistersNothing proves that an
// Engine with no Steering Queue configured (Steering is nil, e.g. New's
// zero value) never registers a loop-id, leaving Registry.Steer resolving
// against an empty registry exactly as before this wiring landed.
func TestExecuteInExecution_NoSteeringQueueRegistersNothing(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"61": {ID: "61"},
	})
	te.fake.ProgramResult("61", agent.AgentResult{Status: agent.StatusImplemented})

	const executionID = "exec-steering-746-nil"
	te.eng.NewExecutionID = func() string { return executionID }

	registry := steering.NewRegistry()
	te.eng.SteeringRegistry = registry
	te.eng.Steering = nil

	ctx := context.Background()
	if _, err := te.eng.Execute(ctx, "61", te.base); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if err := registry.Steer(executionID, "hello"); !errors.Is(err, steering.ErrLoopNotFound) {
		t.Fatalf("registry.Steer(loopID, ...) = %v, want ErrLoopNotFound (no Steering Queue configured, nothing registered)", err)
	}
}

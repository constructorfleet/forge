package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/workspace"
)

// blockingThenImplementingAgent blocks for block with no transcript
// activity — standing in for a wedged local Agent adapter — then returns an
// implemented result.
type blockingThenImplementingAgent struct {
	block time.Duration
}

func (a blockingThenImplementingAgent) Execute(ctx context.Context, _ agent.AgentRequest) (agent.AgentResult, error) {
	select {
	case <-time.After(a.block):
	case <-ctx.Done():
		return agent.AgentResult{}, ctx.Err()
	}
	return agent.AgentResult{Status: agent.StatusImplemented, Summary: "done"}, nil
}

var _ agent.Agent = blockingThenImplementingAgent{}

// TestExecute_WorkerHeartbeatFreezesWhileAgentStalled is the end-to-end
// acceptance case for constructorfleet/forge#463: while a local Execution's
// Agent produces no transcript output for longer than HeartbeatStallAfter,
// workers.last_heartbeat stops advancing — a real "working vs. wedged"
// signal an operator (or the TUI's liveness badge, #453/#494) can read
// long before the Config.Agent.Timeout adapter timeout would fire.
func TestExecute_WorkerHeartbeatFreezesWhileAgentStalled(t *testing.T) {
	repoRoot, base := gittest.NewTempRepo(t)
	store := openTestStore(t)
	trk := &stubTracker{issues: map[string]domain.Issue{"42": {ID: "42"}}}
	mgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}

	const blockFor = 200 * time.Millisecond
	fake := blockingThenImplementingAgent{block: blockFor}
	eng := engine.New(store, trk, &spyWorkspaces{mgr: mgr}, fake, config.Default(), repoRoot)
	eng.NewExecutionID = func() string { return "exec-freeze" }
	eng.HeartbeatInterval = 5 * time.Millisecond
	eng.HeartbeatStallAfter = 20 * time.Millisecond

	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		_, err := eng.Execute(ctx, "42", base)
		done <- err
	}()

	// Let a beat or two land, then snapshot last_heartbeat well after
	// HeartbeatStallAfter has elapsed with no transcript activity: it must
	// have frozen rather than kept ticking on the wall clock.
	time.Sleep(50 * time.Millisecond)
	first, err := store.WorkerClaim(ctx, "exec-freeze", "42")
	if err != nil {
		t.Fatalf("WorkerClaim (first): %v", err)
	}

	time.Sleep(80 * time.Millisecond)
	second, err := store.WorkerClaim(ctx, "exec-freeze", "42")
	if err != nil {
		t.Fatalf("WorkerClaim (second): %v", err)
	}
	if !second.LastHeartbeat.Equal(first.LastHeartbeat) {
		t.Fatalf("last_heartbeat advanced from %v to %v while the Agent was stalled with no transcript output, want frozen past HeartbeatStallAfter", first.LastHeartbeat, second.LastHeartbeat)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Execute never returned")
	}
}

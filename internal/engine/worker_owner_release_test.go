package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/workspace"
)

// blockingUntilCancelAgent enters, signals it has started, then blocks until
// ctx is cancelled — standing in for an Agent whose subprocess is interrupted
// mid-run (e.g. a `forge execute` process that receives SIGINT).
type blockingUntilCancelAgent struct {
	entered chan struct{}
}

func (a *blockingUntilCancelAgent) Execute(ctx context.Context, _ agent.AgentRequest) (agent.AgentResult, error) {
	close(a.entered)
	<-ctx.Done()
	return agent.AgentResult{}, ctx.Err()
}

var _ agent.Agent = (*blockingUntilCancelAgent)(nil)

// TestExecute_InterruptedRun_ClearsWorkerOwner is issue 560's core
// requirement: a Worker interrupted mid-run (its context is cancelled, e.g.
// by SIGINT) must still clear its claim's owner_pid/owner_token rather than
// leaving them stale. ReleaseWorkerClaim runs in a deferred cleanup that
// fires on every return path, including a cancelled one — that cleanup call
// must not itself be defeated by the very cancellation it is cleaning up
// after.
func TestExecute_InterruptedRun_ClearsWorkerOwner(t *testing.T) {
	repoRoot, base := gittest.NewTempRepo(t)
	store := openTestStore(t)
	trk := &stubTracker{issues: map[string]domain.Issue{"42": {ID: "42"}}}
	mgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	fake := &blockingUntilCancelAgent{entered: make(chan struct{})}
	eng := engine.New(store, trk, &spyWorkspaces{mgr: mgr}, fake, config.Default(), repoRoot)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	execution, err := eng.StartExecution(ctx, base)
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, execErr := eng.ExecuteInExecution(ctx, execution, "42", base)
		done <- execErr
	}()

	select {
	case <-fake.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the agent to start")
	}

	cancel()
	if execErr := <-done; execErr == nil {
		t.Fatalf("ExecuteInExecution err = nil, want non-nil (the run was cancelled)")
	}

	if _, err := store.WorkerClaim(context.Background(), execution.ID, "42"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("WorkerClaim (post-cancellation) err = %v, want ErrNotFound — the claim's owner must be released even when the run's own context is cancelled", err)
	}
}

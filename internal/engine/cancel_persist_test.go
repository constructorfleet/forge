package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/workspace"
)

// TestExecute_InterruptedRun_PersistsCancelledState locks in issue 479:
// failOut's cancelled branch must persist CANCELLED even when ctx is
// already cancelled.
func TestExecute_InterruptedRun_PersistsCancelledState(t *testing.T) {
	repoRoot, base := gittest.NewTempRepo(t)
	store := openTestStore(t)
	trk := &stubTracker{issues: map[string]domain.Issue{"84": {ID: "84"}}}
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
		_, execErr := eng.ExecuteInExecution(ctx, execution, "84", base)
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

	issue, err := store.GetIssue(context.Background(), execution.ID, "84")
	if err != nil {
		t.Fatalf("GetIssue (post-cancellation): %v", err)
	}
	if issue.State != domain.StateCancelled {
		t.Fatalf("issue.State = %s, want CANCELLED (failOut's cancelled branch must persist despite the cancelled ctx)", issue.State)
	}
}

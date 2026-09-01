package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/workspace"
)

// stubTrackerForCLI is a minimal engine.IssueFetcher double for cmd/forge
// tests that need Engine wired against something other than the real
// GitHub client (which buildEngine hardcodes to the production API root).
type stubTrackerForCLI struct {
	issue domain.Issue
}

func (s *stubTrackerForCLI) GetIssue(context.Context, string) (domain.Issue, error) {
	return s.issue, nil
}

var _ engine.IssueFetcher = (*stubTrackerForCLI)(nil)

func mustWorkspaceManager(t *testing.T, repoRoot string) *workspace.Manager {
	t.Helper()
	mgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	return mgr
}

func mustConfig() config.Config {
	return config.Default()
}

func newProgrammedFakeAgent(t *testing.T, issueID string) *agent.FakeAgent {
	t.Helper()
	fake := agent.NewFakeAgent()
	fake.ProgramResult(issueID, agent.AgentResult{Status: agent.StatusImplemented, Summary: "done"})
	return fake
}

type recordingLostController struct {
	started  chan struct{}
	stopped  chan struct{}
	interval time.Duration
	err      error
}

func (r *recordingLostController) Run(ctx context.Context, interval time.Duration, _ func(error)) error {
	r.interval = interval
	close(r.started)
	<-ctx.Done()
	r.err = ctx.Err()
	close(r.stopped)
	return r.err
}

func TestStartLostExecutionControllerRunsUntilStopped(t *testing.T) {
	controller := &recordingLostController{
		started: make(chan struct{}),
		stopped: make(chan struct{}),
	}

	stop := startLostExecutionController(context.Background(), controller, nil)
	<-controller.started
	if controller.interval != lostRecoveryPollInterval {
		t.Fatalf("Run interval = %v, want %v", controller.interval, lostRecoveryPollInterval)
	}

	stop()
	<-controller.stopped
	if !errors.Is(controller.err, context.Canceled) {
		t.Fatalf("Run stopped with %v, want context.Canceled", controller.err)
	}
}

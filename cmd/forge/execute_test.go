package main

import (
	"context"
	"testing"

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

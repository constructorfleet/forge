package main

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/workspace"
)

// stubTrackerForCLI is a minimal tracker.Tracker double for cmd/forge tests
// that need Engine wired against something other than the real GitHub
// client (which buildEngine hardcodes to the production API root).
type stubTrackerForCLI struct {
	issue domain.Issue
}

func (s *stubTrackerForCLI) GetIssue(context.Context, string) (domain.Issue, error) {
	return s.issue, nil
}
func (s *stubTrackerForCLI) GetIssues(context.Context, []string) ([]domain.Issue, error) {
	panic("not implemented")
}
func (s *stubTrackerForCLI) GetComments(context.Context, string) ([]tracker.Comment, error) {
	panic("not implemented")
}
func (s *stubTrackerForCLI) AddComment(context.Context, string, string) error {
	panic("not implemented")
}
func (s *stubTrackerForCLI) AddLabel(context.Context, string, string) error {
	panic("not implemented")
}
func (s *stubTrackerForCLI) RemoveLabel(context.Context, string, string) error {
	panic("not implemented")
}
func (s *stubTrackerForCLI) GetMergeRequirements(context.Context, string) (tracker.MergeRequirements, error) {
	panic("not implemented")
}

var _ tracker.Tracker = (*stubTrackerForCLI)(nil)

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

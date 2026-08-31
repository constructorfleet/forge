package localhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/workspace"
)

func newTestBackend(t *testing.T) (*Backend, string, string) {
	t.Helper()
	root, base := gittest.NewTempRepo(t)
	mgr, err := workspace.NewManager(root)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return NewBackend(mgr, agent.NewFakeAgent()), root, base
}

func TestBackend_PrepareCreatesWorkspaceMatchingManager(t *testing.T) {
	backend, root, base := newTestBackend(t)

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{
		ExecutionID: "exec1",
		IssueID:     "issue-42",
		Base:        base,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", root, err)
	}
	wantPath := filepath.Join(resolvedRoot, ".forge", "worktrees", "exec1", "issue-42")

	ws := env.Workspace()
	if ws.Path != wantPath {
		t.Errorf("Workspace().Path = %q, want %q", ws.Path, wantPath)
	}
	if ws.Branch != "forge/exec1/issue-42" {
		t.Errorf("Workspace().Branch = %q, want forge/exec1/issue-42", ws.Branch)
	}
	if ws.IssueID != "issue-42" {
		t.Errorf("Workspace().IssueID = %q, want issue-42", ws.IssueID)
	}
	if info, err := os.Stat(ws.Path); err != nil || !info.IsDir() {
		t.Errorf("worktree dir not created at %s: %v", ws.Path, err)
	}
}

func TestBackend_PrepareIsIdempotent(t *testing.T) {
	backend, _, base := newTestBackend(t)
	req := execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42", Base: base}

	first, err := backend.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare (1st): %v", err)
	}
	second, err := backend.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare (2nd): %v", err)
	}
	if first.Workspace() != second.Workspace() {
		t.Errorf("Workspace mismatch across idempotent Prepare calls: %+v vs %+v", first.Workspace(), second.Workspace())
	}
}

func TestEnvironment_ExecuteRunsSubprocessInWorkspace(t *testing.T) {
	backend, _, base := newTestBackend(t)
	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-42", Base: base,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	result, err := env.Execute(context.Background(), execution.Command{
		Name:    "pwd",
		Command: "pwd",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (stderr: %s)", result.ExitCode, result.Stderr)
	}

	resolvedWSPath, err := filepath.EvalSymlinks(env.Workspace().Path)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	gotDir := filepath.Clean(strings.TrimSpace(result.Stdout))
	if gotDir != resolvedWSPath {
		t.Errorf("Execute ran in %q, want %q", gotDir, resolvedWSPath)
	}
}

func TestEnvironment_ExecuteReportsNonZeroExitCode(t *testing.T) {
	backend, _, base := newTestBackend(t)
	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-42", Base: base,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	result, err := env.Execute(context.Background(), execution.Command{Name: "fail", Command: "exit 3"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", result.ExitCode)
	}
}

func TestEnvironment_AgentReturnsBackendAgent(t *testing.T) {
	root, base := gittest.NewTempRepo(t)
	mgr, err := workspace.NewManager(root)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	fakeAgent := agent.NewFakeAgent()
	backend := NewBackend(mgr, fakeAgent)

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-42", Base: base,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if env.Agent() != fakeAgent {
		t.Errorf("Agent() did not return the backend's Agent")
	}
}

func TestEnvironment_CleanupRemovesWorktree(t *testing.T) {
	backend, _, base := newTestBackend(t)
	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-42", Base: base,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	path := env.Workspace().Path

	if err := env.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("worktree dir still exists at %s after Cleanup: %v", path, err)
	}
}

var _ execution.ExecutionBackend = (*Backend)(nil)

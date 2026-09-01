package container

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/workspace"
)

const testImage = "forge/agent:latest"

func newTestBackend(t *testing.T) (*Backend, *FakeRuntime, string, string) {
	t.Helper()
	root, base := gittest.NewTempRepo(t)
	mgr, err := workspace.NewManager(root)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	runtime := NewFakeRuntime()
	return NewBackend(mgr, runtime, testImage), runtime, root, base
}

func TestBackend_PrepareCreatesWorkspaceMatchingManager(t *testing.T) {
	backend, _, root, base := newTestBackend(t)

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

func TestBackend_PrepareStartsContainerWithWorkspaceBindMounted(t *testing.T) {
	backend, runtime, _, base := newTestBackend(t)

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-42", Base: base,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	started := runtime.Started()
	if len(started) != 1 {
		t.Fatalf("len(Started()) = %d, want 1", len(started))
	}
	spec := started[0]
	if spec.Image != testImage {
		t.Errorf("Image = %q, want %q", spec.Image, testImage)
	}
	if len(spec.Mounts) != 1 {
		t.Fatalf("len(Mounts) = %d, want 1", len(spec.Mounts))
	}
	mount := spec.Mounts[0]
	if mount.HostPath != env.Workspace().Path {
		t.Errorf("Mounts[0].HostPath = %q, want %q", mount.HostPath, env.Workspace().Path)
	}
	if mount.ContainerPath != WorkspaceMountPath {
		t.Errorf("Mounts[0].ContainerPath = %q, want %q", mount.ContainerPath, WorkspaceMountPath)
	}
}

func TestEnvironment_WorkspaceSharesHostGitObjectStore(t *testing.T) {
	backend, _, root, base := newTestBackend(t)

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-42", Base: base,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Commit a new object on the host repository, on a throwaway branch,
	// then check it back out to main so the worktree cannot reach it
	// through its own checked-out branch.
	gittest.RunGit(t, root, "checkout", "-q", "-b", "shared-object-check")
	if err := os.WriteFile(filepath.Join(root, "shared.txt"), []byte("shared\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	gittest.RunGit(t, root, "add", "shared.txt")
	gittest.RunGit(t, root, "commit", "-q", "-m", "shared object store check")
	sha := strings.TrimSpace(gittest.RunGit(t, root, "rev-parse", "HEAD"))
	gittest.RunGit(t, root, "checkout", "-q", "main")
	gittest.RunGit(t, root, "branch", "-D", "shared-object-check")

	// The worktree sees the object immediately, without a fetch, only if
	// it shares the host repository's object store (a real bind mount of
	// the host worktree, not a separate clone).
	out := gittest.RunGit(t, env.Workspace().Path, "cat-file", "-t", sha)
	if strings.TrimSpace(out) != "commit" {
		t.Errorf("cat-file -t %s = %q, want commit (object store not shared)", sha, out)
	}
}

func TestEnvironment_ExecuteIsNotImplementedThisTicket(t *testing.T) {
	backend, _, _, base := newTestBackend(t)
	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-42", Base: base,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if _, err := env.Execute(context.Background(), execution.Command{Name: "noop", Command: "true"}); err == nil {
		t.Error("Execute() error = nil, want error (command execution ships in a later ticket)")
	}
}

func TestEnvironment_CleanupStopsAndRemovesContainerAndWorktree(t *testing.T) {
	backend, runtime, _, base := newTestBackend(t)
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
	if len(runtime.Stopped()) != 1 {
		t.Errorf("len(Stopped()) = %d, want 1", len(runtime.Stopped()))
	}
	if len(runtime.Removed()) != 1 {
		t.Errorf("len(Removed()) = %d, want 1", len(runtime.Removed()))
	}
}

var _ execution.ExecutionBackend = (*Backend)(nil)

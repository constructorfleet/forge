package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
)

// TestWorkspaceEnvironment_ExecuteRunsInsideWorkspacePath covers the
// environment's command primitive: a Command with no WorkDir runs as a real
// subprocess rooted at the Workspace path.
func TestWorkspaceEnvironment_ExecuteRunsInsideWorkspacePath(t *testing.T) {
	dir := t.TempDir()
	env := &workspaceEnvironment{workspace: domain.Workspace{Path: dir}}

	result, err := env.Execute(context.Background(), execution.Command{Name: "pwd", Command: "pwd"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (stderr: %s)", result.ExitCode, result.Stderr)
	}
	if got, want := resolved(t, result.Stdout), resolved(t, dir); got != want {
		t.Errorf("Execute ran in %q, want %q", got, want)
	}
	if result.Name != "pwd" || result.Command != "pwd" {
		t.Errorf("Result = %+v, want the Command's Name and Command echoed back", result)
	}
}

// TestWorkspaceEnvironment_ExecuteRunsInsideWorkDir covers the WorkDir
// field: the command runs in that directory beneath the Workspace.
func TestWorkspaceEnvironment_ExecuteRunsInsideWorkDir(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	env := &workspaceEnvironment{workspace: domain.Workspace{Path: root}}

	result, err := env.Execute(context.Background(), execution.Command{
		Name:    "pwd",
		Command: "pwd",
		WorkDir: "sub",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, want := resolved(t, result.Stdout), resolved(t, filepath.Join(root, "sub")); got != want {
		t.Errorf("Execute ran in %q, want %q", got, want)
	}
}

// resolved cleans path, removes any symbolic links from it, and returns the
// result. A temporary directory on macOS has a symbolic link in its path, so
// tests must compare resolved paths.
func resolved(t *testing.T, path string) string {
	t.Helper()
	clean := filepath.Clean(strings.TrimSpace(path))
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", clean, err)
	}
	return real
}

// TestWorkspaceEnvironment_ExecuteReportsNonZeroExitCode pins the contract
// for a command that runs and fails: the exit code lands in the Result and
// Execute returns no Go error.
func TestWorkspaceEnvironment_ExecuteReportsNonZeroExitCode(t *testing.T) {
	env := &workspaceEnvironment{workspace: domain.Workspace{Path: t.TempDir()}}

	result, err := env.Execute(context.Background(), execution.Command{Name: "fail", Command: "exit 3"})
	if err != nil {
		t.Fatalf("Execute returned error %v, want nil (a failed command is a Result)", err)
	}
	if result.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", result.ExitCode)
	}
}

// TestWorkspaceEnvironment_ExecuteReportsStartFailure pins the other half
// of the contract: a command that cannot start at all reports ExitCode -1
// and returns the error, exactly as the LocalHost backend does.
func TestWorkspaceEnvironment_ExecuteReportsStartFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-directory")
	env := &workspaceEnvironment{workspace: domain.Workspace{Path: missing}}

	result, err := env.Execute(context.Background(), execution.Command{Name: "test", Command: "true"})
	if err == nil {
		t.Fatal("Execute returned nil error, want the start failure")
	}
	if result.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1", result.ExitCode)
	}
}

// TestWorkspaceEnvironment_CleanupDelegatesToWorkspaceCreator covers the
// release half of the seam: Cleanup drives the injected WorkspaceCreator.
func TestWorkspaceEnvironment_CleanupDelegatesToWorkspaceCreator(t *testing.T) {
	cleanup := &recordingWorkspaceCreator{}
	env := &workspaceEnvironment{
		executionID: "exec-1",
		issueID:     "42",
		workspaces:  cleanup,
	}

	if err := env.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !cleanup.cleanupCalled {
		t.Error("Cleanup did not delegate to the WorkspaceCreator")
	}
	if cleanup.executionID != "exec-1" || cleanup.issueID != "42" {
		t.Errorf("Cleanup called with (%s, %s), want (exec-1, 42)", cleanup.executionID, cleanup.issueID)
	}
}

// TestEngineWrapWorkspace_BuildsEnvironmentFromEngineSeams covers the
// resume path: wrapWorkspace wraps an already prepared Workspace with the
// Engine's own WorkspaceCreator and Agent.
func TestEngineWrapWorkspace_BuildsEnvironmentFromEngineSeams(t *testing.T) {
	creator := &recordingWorkspaceCreator{}
	ag := agent.NewFakeAgent()
	e := &Engine{Workspaces: creator, Agent: ag}

	env := e.wrapWorkspace("exec-1", "42", domain.Workspace{Path: "/tmp/ws", Branch: "worker/42"})

	if got := env.Workspace(); got.Path != "/tmp/ws" || got.Branch != "worker/42" {
		t.Errorf("Workspace() = %+v, want the wrapped Workspace", got)
	}
	if env.Agent() != ag {
		t.Error("Agent() did not return the Engine's Agent")
	}
	if err := env.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !creator.cleanupCalled {
		t.Error("Cleanup did not reach the Engine's WorkspaceCreator")
	}
}

// TestEngineBackend_PreparesThroughWorkspaceCreator covers the default
// backend: the Engine prepares its environment through the injected
// WorkspaceCreator instead of calling Create itself.
func TestEngineBackend_PreparesThroughWorkspaceCreator(t *testing.T) {
	creator := &recordingWorkspaceCreator{created: domain.Workspace{Path: "/tmp/ws", Branch: "worker/42"}}
	e := &Engine{Workspaces: creator, Agent: agent.NewFakeAgent()}

	env, err := e.backend().Prepare(context.Background(), execution.WorkspaceRequest{
		ExecutionID: "exec-1",
		IssueID:     "42",
		Base:        "abc123",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := env.Workspace(); got.Path != "/tmp/ws" || got.Branch != "worker/42" {
		t.Errorf("Workspace() = %+v, want the Workspace the WorkspaceCreator created", got)
	}
	if creator.createExecutionID != "exec-1" || creator.createIssueID != "42" || creator.createBase != "abc123" {
		t.Errorf("Create called with (%s, %s, %s), want (exec-1, 42, abc123)", creator.createExecutionID, creator.createIssueID, creator.createBase)
	}
}

// TestEngineBackend_PrefersTheConfiguredBackend pins the override: a wired
// Backend (cmd/forge always wires one) replaces the default.
func TestEngineBackend_PrefersTheConfiguredBackend(t *testing.T) {
	wired := &stubBackend{}
	e := &Engine{Workspaces: &recordingWorkspaceCreator{}, Backend: wired}

	if got := e.backend(); got != execution.ExecutionBackend(wired) {
		t.Errorf("backend() = %T, want the wired Backend", got)
	}
}

// stubBackend is an ExecutionBackend double that records nothing; it only
// has to be distinguishable from the default backend.
type stubBackend struct{}

func (*stubBackend) Prepare(context.Context, execution.WorkspaceRequest) (execution.ExecutionEnvironment, error) {
	return nil, nil
}

var _ execution.ExecutionBackend = (*stubBackend)(nil)

// recordingWorkspaceCreator is a minimal engine.WorkspaceCreator double for
// this file's environment-level tests.
type recordingWorkspaceCreator struct {
	created domain.Workspace

	cleanupCalled bool
	executionID   string
	issueID       string

	createExecutionID, createIssueID, createBase string
}

func (r *recordingWorkspaceCreator) Create(_ context.Context, executionID, issueID, base string) (domain.Workspace, error) {
	r.createExecutionID, r.createIssueID, r.createBase = executionID, issueID, base
	return r.created, nil
}

func (r *recordingWorkspaceCreator) Cleanup(_ context.Context, executionID, issueID string) error {
	r.cleanupCalled = true
	r.executionID, r.issueID = executionID, issueID
	return nil
}

func (r *recordingWorkspaceCreator) Validate(_ context.Context, _, _ string) (domain.Workspace, error) {
	return domain.Workspace{}, nil
}

var _ WorkspaceCreator = (*recordingWorkspaceCreator)(nil)

package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/gate/gatetest"
)

// TestCommandRunnerEnvironment_ExecuteRunsInsideWorkspacePath covers
// ticket 301's "Quality Gates run through env.Execute" acceptance
// criterion at the adapter seam: a Command with no WorkDir runs rooted at
// the Workspace path the environment was built with.
func TestCommandRunnerEnvironment_ExecuteRunsInsideWorkspacePath(t *testing.T) {
	runner := gatetest.NewFakeCommandRunner()
	runner.ProgramResult("make test", 0, "tests ok", "")

	env := &commandRunnerEnvironment{
		executionID: "exec-1",
		issueID:     "42",
		workspace:   domain.Workspace{Path: "/tmp/workspace-42"},
		command:     runner,
	}

	result, err := env.Execute(context.Background(), execution.Command{Name: "test", Command: "make test"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Stdout != "tests ok" {
		t.Errorf("Stdout = %q, want %q", result.Stdout, "tests ok")
	}
	if got, want := runner.WorkDirs(), []string{"/tmp/workspace-42"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("WorkDirs = %v, want %v", got, want)
	}
}

// TestCommandRunnerEnvironment_ExecuteFoldsCommandRunnerErrorIntoFailure
// mirrors gate.Runner.runOne's documented contract (internal/gate/gate.go):
// a CommandRunner error (the command could not even be started) is folded
// into a failing Result with ExitCode -1, not returned as a Go error — a
// misconfigured gate command is diagnosed via the Result, not a crashed
// run.
func TestCommandRunnerEnvironment_ExecuteFoldsCommandRunnerErrorIntoFailure(t *testing.T) {
	runner := gatetest.NewFakeCommandRunner()
	runner.ProgramError("make test", errors.New("boom: could not start"))

	env := &commandRunnerEnvironment{
		workspace: domain.Workspace{Path: "/tmp/workspace-42"},
		command:   runner,
	}

	result, err := env.Execute(context.Background(), execution.Command{Name: "test", Command: "make test"})
	if err != nil {
		t.Fatalf("Execute returned error %v, want nil (folded into Result)", err)
	}
	if result.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1", result.ExitCode)
	}
}

// TestCommandRunnerEnvironment_CleanupDelegatesToWorkspaceCreator covers
// the other half of ticket 301's acceptance criterion: releasing the
// Workspace goes through the environment's Cleanup, which in turn drives
// the injected WorkspaceCreator.
func TestCommandRunnerEnvironment_CleanupDelegatesToWorkspaceCreator(t *testing.T) {
	cleanup := &recordingWorkspaceCreator{}
	env := &commandRunnerEnvironment{
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

// TestWorkspaceCreatorBackend_PrepareCreatesWorkspaceViaWorkspaceCreator
// covers ticket 301's "the Engine acquires ... its Workspace via
// backend.Prepare" acceptance criterion: Prepare creates the Workspace
// through the injected WorkspaceCreator and returns an environment wrapping
// it.
func TestWorkspaceCreatorBackend_PrepareCreatesWorkspaceViaWorkspaceCreator(t *testing.T) {
	creator := &recordingWorkspaceCreator{created: domain.Workspace{Path: "/tmp/ws", Branch: "worker/42"}}
	backend := &workspaceCreatorBackend{workspaces: creator}

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{
		ExecutionID: "exec-1",
		IssueID:     "42",
		Base:        "abc123",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := env.Workspace(); got.Path != "/tmp/ws" || got.Branch != "worker/42" {
		t.Errorf("Workspace() = %+v, want the Workspace created by the WorkspaceCreator", got)
	}
	if creator.createExecutionID != "exec-1" || creator.createIssueID != "42" || creator.createBase != "abc123" {
		t.Errorf("Create called with (%s, %s, %s), want (exec-1, 42, abc123)", creator.createExecutionID, creator.createIssueID, creator.createBase)
	}
}

// recordingWorkspaceCreator is a minimal engine.WorkspaceCreator double for
// this file's adapter-level tests.
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

// Package localhost implements the first ExecutionBackend: an in-process
// backend that reproduces Forge's existing behavior — a git-worktree
// Workspace (via internal/workspace.Manager) and local subprocesses (via
// os/exec) — behind the neutral execution.ExecutionBackend seam.
package localhost

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/workspace"
)

// waitDelay bounds how long Execute waits, after the command's own process
// exits, for the io-copy goroutines feeding stdout/stderr to see EOF. See
// gate.ExecCommandRunner's doc comment (internal/gate/exec.go) for why this
// is needed and https://pkg.go.dev/os/exec#Cmd.WaitDelay.
const waitDelay = 2 * time.Second

// Backend is the LocalHost ExecutionBackend: it prepares Workspaces via a
// *workspace.Manager and hands every environment the same Agent.
type Backend struct {
	workspaces *workspace.Manager
	agent      agent.Agent
}

// NewBackend returns a Backend that prepares Workspaces via workspaces and
// gives every prepared environment ag as its Agent.
func NewBackend(workspaces *workspace.Manager, ag agent.Agent) *Backend {
	return &Backend{workspaces: workspaces, agent: ag}
}

// Prepare creates (or, per workspace.Manager.Create, idempotently reuses)
// the git-worktree Workspace for req.
func (b *Backend) Prepare(ctx context.Context, req execution.WorkspaceRequest) (execution.ExecutionEnvironment, error) {
	ws, err := b.workspaces.Create(ctx, req.ExecutionID, req.IssueID, req.Base)
	if err != nil {
		return nil, err
	}
	return &environment{
		executionID: req.ExecutionID,
		issueID:     req.IssueID,
		workspace:   ws,
		workspaces:  b.workspaces,
		agent:       b.agent,
	}, nil
}

// environment is the LocalHost ExecutionEnvironment: one prepared
// git-worktree Workspace, run in-process for the lifetime of one Issue
// execution.
type environment struct {
	executionID string
	issueID     string
	workspace   domain.Workspace
	workspaces  *workspace.Manager
	agent       agent.Agent
}

// Workspace returns the Workspace this environment prepared.
func (e *environment) Workspace() domain.Workspace {
	return e.workspace
}

// Execute runs cmd as a real subprocess via `sh -c`, rooted at the
// Workspace (or cmd.WorkDir beneath it), and captures its stdout/stderr.
func (e *environment) Execute(ctx context.Context, cmd execution.Command) (execution.Result, error) {
	dir := e.workspace.Path
	if cmd.WorkDir != "" {
		dir = filepath.Join(dir, cmd.WorkDir)
	}

	result := execution.Result{Name: cmd.Name, Command: cmd.Command, StartedAt: time.Now()}

	var execCmd *exec.Cmd
	if len(cmd.Args) > 0 {
		execCmd = exec.CommandContext(ctx, cmd.Args[0], cmd.Args[1:]...)
	} else {
		execCmd = exec.CommandContext(ctx, "sh", "-c", cmd.Command)
	}
	execCmd.Dir = dir
	execCmd.WaitDelay = waitDelay
	if cmd.Stdin != "" {
		execCmd.Stdin = strings.NewReader(cmd.Stdin)
	}
	if cmd.Env != nil {
		execCmd.Env = cmd.Env
	}
	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	runErr := execCmd.Run()
	result.FinishedAt = time.Now()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()

	if runErr == nil {
		result.ExitCode = 0
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	result.ExitCode = -1
	return result, runErr
}

// Agent returns the Backend's Agent.
func (e *environment) Agent() agent.Agent {
	return e.agent
}

// Cleanup removes the Workspace's git worktree and branch.
func (e *environment) Cleanup(ctx context.Context) error {
	return e.workspaces.Cleanup(ctx, e.executionID, e.issueID)
}

var _ execution.ExecutionEnvironment = (*environment)(nil)

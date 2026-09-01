package engine

import (
	"bytes"
	"context"
	"path/filepath"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/gate"
)

// commandRunnerEnvironment adapts Engine's existing WorkspaceCreator and
// gate.CommandRunner seams to the execution.ExecutionEnvironment interface
// (ticket 301, constructorfleet/forge#285): it lets the Engine drive
// Workspace lifecycle and Quality Gates through the environment seam
// without changing the WorkspaceCreator/Gates injection points those seams
// already have. It is retired in favor of a real backend by ticket 305
// ("remove the redundant separate command-runner injection").
type commandRunnerEnvironment struct {
	executionID, issueID string
	workspace            domain.Workspace
	workspaces           WorkspaceCreator
	command              gate.CommandRunner
	ag                   agent.Agent
}

var _ execution.ExecutionEnvironment = (*commandRunnerEnvironment)(nil)

// Workspace returns the Workspace this environment was built with.
func (e *commandRunnerEnvironment) Workspace() domain.Workspace {
	return e.workspace
}

// Execute runs cmd through the injected gate.CommandRunner, rooted at the
// Workspace (or cmd.WorkDir beneath it). It mirrors gate.Runner.runOne's
// documented contract: a CommandRunner error (the command could not even be
// started) is folded into a failing Result with ExitCode -1, never returned
// as a Go error, so a misconfigured gate command is diagnosed via the
// Result rather than a crashed orchestration run.
func (e *commandRunnerEnvironment) Execute(ctx context.Context, cmd execution.Command) (execution.Result, error) {
	dir := e.workspace.Path
	if cmd.WorkDir != "" {
		dir = filepath.Join(dir, cmd.WorkDir)
	}

	result := execution.Result{Name: cmd.Name, Command: cmd.Command, StartedAt: time.Now()}

	var stdout, stderr bytes.Buffer
	exitCode, err := e.command.Run(ctx, dir, cmd.Command, &stdout, &stderr)
	result.FinishedAt = time.Now()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()

	if err != nil {
		exitCode = -1
		result.Stderr += "\ngate runner: " + err.Error()
	}
	result.ExitCode = exitCode

	return result, nil
}

// Agent returns the Agent this environment was built with.
func (e *commandRunnerEnvironment) Agent() agent.Agent {
	return e.ag
}

// Cleanup releases the Workspace through the injected WorkspaceCreator.
func (e *commandRunnerEnvironment) Cleanup(ctx context.Context) error {
	return e.workspaces.Cleanup(ctx, e.executionID, e.issueID)
}

// workspaceCreatorBackend adapts a WorkspaceCreator/gate.CommandRunner/
// agent.Agent trio to execution.ExecutionBackend, so Engine can obtain its
// Workspace via Prepare instead of calling WorkspaceCreator.Create
// directly. See commandRunnerEnvironment's doc comment for the same
// ticket-301/305 context.
type workspaceCreatorBackend struct {
	workspaces WorkspaceCreator
	command    gate.CommandRunner
	ag         agent.Agent
}

var _ execution.ExecutionBackend = (*workspaceCreatorBackend)(nil)

// Prepare creates the Workspace req describes via the injected
// WorkspaceCreator and returns an environment wrapping it.
func (b *workspaceCreatorBackend) Prepare(ctx context.Context, req execution.WorkspaceRequest) (execution.ExecutionEnvironment, error) {
	ws, err := b.workspaces.Create(ctx, req.ExecutionID, req.IssueID, req.Base)
	if err != nil {
		return nil, err
	}
	return &commandRunnerEnvironment{
		executionID: req.ExecutionID,
		issueID:     req.IssueID,
		workspace:   ws,
		workspaces:  b.workspaces,
		command:     b.command,
		ag:          b.ag,
	}, nil
}

// backend returns the ExecutionBackend the Engine prepares new environments
// from, built from the Engine's current WorkspaceCreator/Gates/Agent (read
// at call time, so tests that assign te.eng.Gates after construction still
// take effect).
func (e *Engine) backend() execution.ExecutionBackend {
	return &workspaceCreatorBackend{workspaces: e.Workspaces, command: e.Gates, ag: e.Agent}
}

// wrapWorkspace wraps an already-prepared Workspace (e.g. one reloaded via
// WorkspaceCreator.Validate for a resumed run) in the same environment type
// Prepare returns, so a resumed run's Quality Gates also execute through
// env.Execute.
func (e *Engine) wrapWorkspace(executionID, issueID string, ws domain.Workspace) execution.ExecutionEnvironment {
	return &commandRunnerEnvironment{
		executionID: executionID,
		issueID:     issueID,
		workspace:   ws,
		workspaces:  e.Workspaces,
		command:     e.Gates,
		ag:          e.Agent,
	}
}

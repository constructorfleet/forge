package engine_test

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/gate"
)

// gateCommandSwitch is the gate.CommandRunner every command from
// fakeBackend's environments runs through. A test installs its own runner
// with Set, so the test never shells out. The default runner is the real
// subprocess runner, which is what a test that installs nothing gets.
//
// The Engine has one command primitive: the environment's Execute (ticket
// 305, constructorfleet/forge#285). This switch is how the tests keep
// control of that primitive, so it lives here in the test package rather
// than in the Engine.
type gateCommandSwitch struct {
	mu     sync.Mutex
	runner gate.CommandRunner
}

func newGateCommandSwitch() *gateCommandSwitch {
	return &gateCommandSwitch{runner: gate.ExecCommandRunner{}}
}

// Set installs runner as the runner for every later command.
func (s *gateCommandSwitch) Set(runner gate.CommandRunner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runner = runner
}

// Run executes command through the installed runner.
func (s *gateCommandSwitch) Run(ctx context.Context, workDir, command string, stdout, stderr io.Writer) (int, error) {
	s.mu.Lock()
	runner := s.runner
	s.mu.Unlock()
	return runner.Run(ctx, workDir, command, stdout, stderr)
}

var _ gate.CommandRunner = (*gateCommandSwitch)(nil)

// fakeBackend is the ExecutionBackend the engine tests prepare their
// environments from. It creates each Workspace with the test's
// WorkspaceCreator and executes each command through the test's
// gateCommandSwitch. It reads the Agent from the Engine at Prepare time, so
// a test that replaces eng.Agent after construction still takes effect.
type fakeBackend struct {
	eng        *engine.Engine
	workspaces engine.WorkspaceCreator
	commands   *gateCommandSwitch

	// prepareErr, when set, makes Prepare fail immediately with this error
	// instead of creating a Workspace — the seam
	// TestExecute_PrepareError_RoutesThroughFailOutWithoutHanging uses to
	// simulate a backend (e.g. Container) whose environment never comes up.
	prepareErr error
}

var _ execution.ExecutionBackend = (*fakeBackend)(nil)

func (b *fakeBackend) Prepare(ctx context.Context, req execution.WorkspaceRequest) (execution.ExecutionEnvironment, error) {
	if b.prepareErr != nil {
		return nil, b.prepareErr
	}
	ws, err := b.workspaces.Create(ctx, req.ExecutionID, req.IssueID, req.Base)
	if err != nil {
		return nil, err
	}
	return &fakeEnvironment{
		executionID: req.ExecutionID,
		issueID:     req.IssueID,
		workspace:   ws,
		workspaces:  b.workspaces,
		eng:         b.eng,
		commands:    b.commands,
	}, nil
}

// fakeEnvironment is fakeBackend's ExecutionEnvironment.
type fakeEnvironment struct {
	executionID string
	issueID     string
	workspace   domain.Workspace
	workspaces  engine.WorkspaceCreator
	eng         *engine.Engine
	commands    *gateCommandSwitch
}

var _ execution.ExecutionEnvironment = (*fakeEnvironment)(nil)

func (e *fakeEnvironment) Workspace() domain.Workspace {
	return e.workspace
}

// Execute runs cmd through the test's gateCommandSwitch, rooted at the
// Workspace (or cmd.WorkDir below it). A runner error reports ExitCode -1
// and is returned as an error, exactly as the real environments do (a
// command that cannot run at all, e.g. a lost container, is not the same
// as a command that ran and exited non-zero).
func (e *fakeEnvironment) Execute(ctx context.Context, cmd execution.Command) (execution.Result, error) {
	dir := e.workspace.Path
	if cmd.WorkDir != "" {
		dir = filepath.Join(dir, cmd.WorkDir)
	}

	result := execution.Result{Name: cmd.Name, Command: cmd.Command, StartedAt: time.Now()}

	var stdout, stderr bytes.Buffer
	exitCode, err := e.commands.Run(ctx, dir, cmd.Command, &stdout, &stderr)
	result.FinishedAt = time.Now()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	result.ExitCode = exitCode
	if err != nil {
		result.ExitCode = -1
		return result, err
	}

	return result, nil
}

func (e *fakeEnvironment) Agent() agent.Agent {
	return e.eng.Agent
}

func (e *fakeEnvironment) Cleanup(ctx context.Context) error {
	return e.workspaces.Cleanup(ctx, e.executionID, e.issueID)
}

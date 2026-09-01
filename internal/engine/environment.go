package engine

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
)

// waitDelay bounds how long Execute waits, after the command's own process
// exits, for the io-copy goroutines that feed stdout/stderr to see EOF. See
// gate.ExecCommandRunner's doc comment (internal/gate/exec.go) for the
// reason and https://pkg.go.dev/os/exec#Cmd.WaitDelay.
const waitDelay = 2 * time.Second

// workspaceEnvironment is the ExecutionEnvironment the Engine builds around
// one Workspace. Execute is the single command primitive (ticket 305,
// constructorfleet/forge#285): it runs each command as a local subprocess,
// exactly as the LocalHost backend does (internal/execution/localhost).
// The Engine keeps no separate command-runner seam.
type workspaceEnvironment struct {
	executionID, issueID string
	workspace            domain.Workspace
	workspaces           WorkspaceCreator
	ag                   agent.Agent
}

var _ execution.ExecutionEnvironment = (*workspaceEnvironment)(nil)

// Workspace returns the Workspace this environment was built with.
func (e *workspaceEnvironment) Workspace() domain.Workspace {
	return e.workspace
}

// Execute runs cmd as a subprocess through `sh -c`, rooted at the Workspace
// (or cmd.WorkDir below it), and captures its stdout and stderr. A command
// that runs and fails reports its exit code in the Result and returns no
// error. A command that cannot start reports ExitCode -1 and returns the
// error.
func (e *workspaceEnvironment) Execute(ctx context.Context, cmd execution.Command) (execution.Result, error) {
	dir := e.workspace.Path
	if cmd.WorkDir != "" {
		dir = filepath.Join(dir, cmd.WorkDir)
	}

	result := execution.Result{Name: cmd.Name, Command: cmd.Command, StartedAt: time.Now()}

	execCmd := exec.CommandContext(ctx, "sh", "-c", cmd.Command)
	execCmd.Dir = dir
	execCmd.WaitDelay = waitDelay
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

// Agent returns the Agent this environment was built with.
func (e *workspaceEnvironment) Agent() agent.Agent {
	return e.ag
}

// Cleanup releases the Workspace through the injected WorkspaceCreator.
func (e *workspaceEnvironment) Cleanup(ctx context.Context) error {
	return e.workspaces.Cleanup(ctx, e.executionID, e.issueID)
}

// workspaceCreatorBackend is the default ExecutionBackend: it prepares each
// environment through the WorkspaceCreator the caller injected into New.
// The Engine itself never calls WorkspaceCreator.Create (ticket 305,
// constructorfleet/forge#285): the backend owns that call, exactly as the
// LocalHost backend owns its workspace.Manager.Create call.
type workspaceCreatorBackend struct {
	workspaces WorkspaceCreator
	ag         agent.Agent
}

var _ execution.ExecutionBackend = (*workspaceCreatorBackend)(nil)

// Prepare creates the Workspace req describes through the injected
// WorkspaceCreator and returns an environment around it.
func (b *workspaceCreatorBackend) Prepare(ctx context.Context, req execution.WorkspaceRequest) (execution.ExecutionEnvironment, error) {
	ws, err := b.workspaces.Create(ctx, req.ExecutionID, req.IssueID, req.Base)
	if err != nil {
		return nil, err
	}
	return &workspaceEnvironment{
		executionID: req.ExecutionID,
		issueID:     req.IssueID,
		workspace:   ws,
		workspaces:  b.workspaces,
		ag:          b.ag,
	}, nil
}

// backend returns the ExecutionBackend the Engine prepares new environments
// from. cmd/forge selects one from Config.Execution.Backend (issue #304) and
// assigns it to e.Backend. A nil e.Backend gets the default backend above,
// built from the Engine's current WorkspaceCreator and Agent, so every
// caller of New keeps a working execution seam without extra wiring.
func (e *Engine) backend() execution.ExecutionBackend {
	if e.Backend != nil {
		return e.Backend
	}
	return &workspaceCreatorBackend{workspaces: e.Workspaces, ag: e.Agent}
}

// wrapWorkspace wraps an already prepared Workspace (for example one that
// WorkspaceCreator.Validate reloaded for a resumed run) in the same
// environment type Prepare returns. A resumed run therefore executes every
// command through env.Execute too.
func (e *Engine) wrapWorkspace(executionID, issueID string, ws domain.Workspace) execution.ExecutionEnvironment {
	return &workspaceEnvironment{
		executionID: executionID,
		issueID:     issueID,
		workspace:   ws,
		workspaces:  e.Workspaces,
		ag:          e.Agent,
	}
}

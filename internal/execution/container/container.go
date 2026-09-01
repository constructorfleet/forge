package container

import (
	"context"
	"errors"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/workspace"
)

// errExecuteNotImplemented is environment.Execute's outcome in this ticket.
// A later ticket adds command execution inside the container.
var errExecuteNotImplemented = errors.New("container: Execute is not implemented yet")

// Backend is the Container ExecutionBackend: it prepares a host git-worktree
// Workspace via a *workspace.Manager, then launches an isolated container
// from image, through a ContainerRuntime, with that Workspace bind-mounted.
type Backend struct {
	workspaces *workspace.Manager
	runtime    ContainerRuntime
	image      string
}

// NewBackend returns a Backend that prepares Workspaces via workspaces and
// launches every environment's container from image, through runtime.
func NewBackend(workspaces *workspace.Manager, runtime ContainerRuntime, image string) *Backend {
	return &Backend{workspaces: workspaces, runtime: runtime, image: image}
}

// Prepare creates (or, per workspace.Manager.Create, idempotently reuses)
// the git-worktree Workspace for req, then launches a container from the
// Backend's image with that Workspace bind-mounted at WorkspaceMountPath.
// Because the Workspace is a git worktree, the bind mount shares the host
// repository's git object store with the container.
func (b *Backend) Prepare(ctx context.Context, req execution.WorkspaceRequest) (execution.ExecutionEnvironment, error) {
	ws, err := b.workspaces.Create(ctx, req.ExecutionID, req.IssueID, req.Base)
	if err != nil {
		return nil, err
	}

	handle, err := b.runtime.Start(ctx, ContainerSpec{
		Image:  b.image,
		Mounts: []Mount{{HostPath: ws.Path, ContainerPath: WorkspaceMountPath}},
	})
	if err != nil {
		return nil, err
	}

	return &environment{
		executionID: req.ExecutionID,
		issueID:     req.IssueID,
		workspace:   ws,
		workspaces:  b.workspaces,
		runtime:     b.runtime,
		handle:      handle,
	}, nil
}

// environment is the Container ExecutionEnvironment: one prepared
// git-worktree Workspace, bind-mounted into one running container, for the
// lifetime of one Issue execution.
type environment struct {
	executionID string
	issueID     string
	workspace   domain.Workspace
	workspaces  *workspace.Manager
	runtime     ContainerRuntime
	handle      ContainerHandle
}

// Workspace returns the Workspace this environment prepared.
func (e *environment) Workspace() domain.Workspace {
	return e.workspace
}

// Execute is not implemented in this ticket. A later ticket runs cmd inside
// the container, through the ContainerRuntime's Exec.
func (e *environment) Execute(_ context.Context, _ execution.Command) (execution.Result, error) {
	return execution.Result{}, errExecuteNotImplemented
}

// Agent is not implemented in this ticket. A later ticket returns an Agent
// that runs inside the container.
func (e *environment) Agent() agent.Agent {
	return nil
}

// Cleanup stops and removes the environment's container, then removes the
// Workspace's git worktree and branch. It attempts every step even if an
// earlier one fails, and joins every error encountered.
func (e *environment) Cleanup(ctx context.Context) error {
	stopErr := e.runtime.Stop(ctx, e.handle)
	removeErr := e.runtime.Remove(ctx, e.handle)
	workspaceErr := e.workspaces.Cleanup(ctx, e.executionID, e.issueID)
	return errors.Join(stopErr, removeErr, workspaceErr)
}

var (
	_ execution.ExecutionBackend     = (*Backend)(nil)
	_ execution.ExecutionEnvironment = (*environment)(nil)
)

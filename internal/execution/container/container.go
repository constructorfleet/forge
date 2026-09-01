package container

import (
	"context"
	"errors"
	"fmt"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/workspace"
)

// Backend is the Container ExecutionBackend: it prepares a host git-worktree
// Workspace via a *workspace.Manager, then launches an isolated container
// from image, through a ContainerRuntime, with that Workspace bind-mounted.
type Backend struct {
	workspaces *workspace.Manager
	runtime    ContainerRuntime
	image      string
	resources  Resources
	newAgent   AgentFactory
}

// NewBackend returns a Backend that prepares Workspaces via workspaces,
// launches every environment's container from image, with resources,
// through runtime, and gives every prepared environment the Agent newAgent
// builds for it. newAgent may be nil, in which case Agent() returns nil.
func NewBackend(workspaces *workspace.Manager, runtime ContainerRuntime, image string, resources Resources, newAgent AgentFactory) *Backend {
	return &Backend{workspaces: workspaces, runtime: runtime, image: image, resources: resources, newAgent: newAgent}
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
		CPU:    b.resources.CPU,
		Memory: b.resources.Memory,
		Mounts: []Mount{{HostPath: ws.Path, ContainerPath: WorkspaceMountPath}},
	})
	if err != nil {
		// Start failed after Create already made the worktree above, so
		// this environment never comes up and the Engine gets no
		// ExecutionEnvironment to Cleanup (constructorfleet/forge#337):
		// the Backend must remove the partial worktree itself here.
		startErr := fmt.Errorf("container: start container: %w", err)
		if cleanupErr := b.workspaces.Cleanup(ctx, req.ExecutionID, req.IssueID); cleanupErr != nil {
			return nil, errors.Join(startErr, fmt.Errorf("container: cleanup worktree after start failure: %w", cleanupErr))
		}
		return nil, startErr
	}

	return &environment{
		executionID: req.ExecutionID,
		issueID:     req.IssueID,
		workspace:   ws,
		workspaces:  b.workspaces,
		runtime:     b.runtime,
		handle:      handle,
		newAgent:    b.newAgent,
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
	newAgent    AgentFactory
}

// Workspace returns the Workspace this environment prepared.
func (e *environment) Workspace() domain.Workspace {
	return e.workspace
}

// Execute runs cmd inside the environment's container, against the
// mounted Workspace, through the ContainerRuntime's Exec. This is the seam
// Quality Gates and Git plumbing that must touch the Workspace (stage,
// commit) run through, so those operations happen in-container rather than
// on the host.
func (e *environment) Execute(ctx context.Context, cmd execution.Command) (execution.Result, error) {
	return e.runtime.Exec(ctx, e.handle, cmd)
}

// Agent returns the Agent the Backend's AgentFactory builds for this
// environment, or nil if the Backend has no AgentFactory.
func (e *environment) Agent() agent.Agent {
	if e.newAgent == nil {
		return nil
	}
	return e.newAgent(e)
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

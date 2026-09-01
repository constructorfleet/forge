package remote

import (
	"context"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
)

// Backend is the Remote ExecutionBackend: it prepares Workspaces on a
// configured worker, through the WorkerClient seam, instead of in-process.
type Backend struct {
	worker WorkerClient
}

// NewBackend returns a Backend that drives worker through the WorkerClient
// seam.
func NewBackend(worker WorkerClient) *Backend {
	return &Backend{worker: worker}
}

// Prepare has the worker fetch the repository read-only at req.Base and
// returns an environment that drives every later operation on that worker.
func (b *Backend) Prepare(ctx context.Context, req execution.WorkspaceRequest) (execution.ExecutionEnvironment, error) {
	handle, ws, err := b.worker.PrepareWorkspace(ctx, req)
	if err != nil {
		return nil, err
	}
	return &environment{worker: b.worker, handle: handle, workspace: ws}, nil
}

// environment is the Remote ExecutionEnvironment: one Workspace prepared on
// a worker, for the lifetime of one Issue execution. Every Command and the
// Agent run on the worker, not in-process.
type environment struct {
	worker    WorkerClient
	handle    WorkerHandle
	workspace domain.Workspace
}

// Workspace returns the Workspace the worker prepared.
func (e *environment) Workspace() domain.Workspace {
	return e.workspace
}

// Execute runs cmd on the worker, inside the prepared Workspace.
func (e *environment) Execute(ctx context.Context, cmd execution.Command) (execution.Result, error) {
	return e.worker.Execute(ctx, e.handle, cmd)
}

// Agent returns an agent.Agent that runs on the worker, inside the prepared
// Workspace.
func (e *environment) Agent() agent.Agent {
	return &remoteAgent{worker: e.worker, handle: e.handle}
}

// Cleanup tears down the worker's Workspace.
func (e *environment) Cleanup(ctx context.Context) error {
	return e.worker.Cleanup(ctx, e.handle)
}

// remoteAgent adapts a WorkerClient's RunAgent call to the agent.Agent
// seam, so an environment can hand it out as a normal Agent.
type remoteAgent struct {
	worker WorkerClient
	handle WorkerHandle
}

// Execute runs req on the worker, inside the Workspace handle identifies.
func (a *remoteAgent) Execute(ctx context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	return a.worker.RunAgent(ctx, a.handle, req)
}

var (
	_ execution.ExecutionBackend     = (*Backend)(nil)
	_ execution.ExecutionEnvironment = (*environment)(nil)
	_ agent.Agent                    = (*remoteAgent)(nil)
)

package remote

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
)

// RecoverFunc checks whether the ExecutionLease for executionID/issueID has
// lapsed and, if so, performs the LOST recovery (expire the lease, mark the
// ExecutionPlacement non-authoritative, and retry the Issue under its
// existing retry budget) exactly as engine.RecoverLostExecution does. It
// reports lost=true only when recovery ran because the lease had lapsed. A
// nil RecoverFunc disables loss detection entirely: a WorkerClient error
// then always surfaces as an ordinary failure, matching every
// ExecutionBackend before this seam existed.
type RecoverFunc func(ctx context.Context, executionID, issueID string) (lost bool, err error)

// Backend is the Remote ExecutionBackend: it prepares Workspaces on a
// configured worker, through the WorkerClient seam, instead of in-process.
type Backend struct {
	worker  WorkerClient
	recover RecoverFunc
}

// NewBackend returns a Backend that drives worker through the WorkerClient
// seam. recover distinguishes a vanished worker (heartbeat lapse) from a
// worker-reported failure; pass nil to disable loss detection.
func NewBackend(worker WorkerClient, recover RecoverFunc) *Backend {
	return &Backend{worker: worker, recover: recover}
}

// Prepare has the worker fetch the repository read-only at req.Base and
// returns an environment that drives every later operation on that worker.
func (b *Backend) Prepare(ctx context.Context, req execution.WorkspaceRequest) (execution.ExecutionEnvironment, error) {
	handle, ws, err := b.worker.PrepareWorkspace(ctx, req)
	if err != nil {
		return nil, err
	}
	return &environment{
		worker:      b.worker,
		handle:      handle,
		workspace:   ws,
		executionID: req.ExecutionID,
		issueID:     req.IssueID,
		recover:     b.recover,
	}, nil
}

// environment is the Remote ExecutionEnvironment: one Workspace prepared on
// a worker, for the lifetime of one Issue execution. Every Command and the
// Agent run on the worker, not in-process.
type environment struct {
	worker      WorkerClient
	handle      WorkerHandle
	workspace   domain.Workspace
	executionID string
	issueID     string
	recover     RecoverFunc
}

// classifyErr consults recover (if configured) on a non-nil WorkerClient
// error, distinguishing a lost worker from an ordinary transport failure. A
// nil err (the worker responded, whether or not its Result/AgentResult
// reports a failure) never reaches recover: a reported failure is a value,
// not an error, and is never a candidate for LOST.
func classifyErr(ctx context.Context, recover RecoverFunc, executionID, issueID string, err error) error {
	if err == nil || recover == nil {
		return err
	}
	lost, recoverErr := recover(ctx, executionID, issueID)
	if recoverErr != nil {
		return fmt.Errorf("remote: recover lost execution %s/%s: %w", executionID, issueID, recoverErr)
	}
	if lost {
		return fmt.Errorf("remote: worker lost for %s/%s: %w: %w", executionID, issueID, execution.ErrLost, err)
	}
	return err
}

// Workspace returns the Workspace the worker prepared.
func (e *environment) Workspace() domain.Workspace {
	return e.workspace
}

// Execute runs cmd on the worker, inside the prepared Workspace. A
// WorkerClient transport error is classified against recover before it
// reaches the caller: a lapsed lease wraps execution.ErrLost, everything
// else (including a nil recover) passes through unchanged.
func (e *environment) Execute(ctx context.Context, cmd execution.Command) (execution.Result, error) {
	result, err := e.worker.Execute(ctx, e.handle, cmd)
	return result, classifyErr(ctx, e.recover, e.executionID, e.issueID, err)
}

// Agent returns an agent.Agent that runs on the worker, inside the prepared
// Workspace.
func (e *environment) Agent() agent.Agent {
	return &remoteAgent{worker: e.worker, handle: e.handle, executionID: e.executionID, issueID: e.issueID, recover: e.recover}
}

// Cleanup tears down the worker's Workspace.
func (e *environment) Cleanup(ctx context.Context) error {
	return e.worker.Cleanup(ctx, e.handle)
}

// remoteAgent adapts a WorkerClient's RunAgent call to the agent.Agent
// seam, so an environment can hand it out as a normal Agent.
type remoteAgent struct {
	worker      WorkerClient
	handle      WorkerHandle
	executionID string
	issueID     string
	recover     RecoverFunc
}

// Execute runs req on the worker, inside the Workspace handle identifies.
// A WorkerClient transport error is classified against recover exactly as
// environment.Execute does: a lapsed lease wraps execution.ErrLost, an
// Agent-reported failure (agent.StatusFailed, no error) never reaches
// recover at all.
func (a *remoteAgent) Execute(ctx context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	result, err := a.worker.RunAgent(ctx, a.handle, req)
	return result, classifyErr(ctx, a.recover, a.executionID, a.issueID, err)
}

var (
	_ execution.ExecutionBackend     = (*Backend)(nil)
	_ execution.ExecutionEnvironment = (*environment)(nil)
	_ agent.Agent                    = (*remoteAgent)(nil)
)

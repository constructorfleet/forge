// Package remote implements the Remote ExecutionBackend: it prepares a
// Workspace on a separate worker and drives commands and the Agent there
// through the WorkerClient seam, instead of running them in-process.
package remote

import (
	"context"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
)

// WorkerHandle identifies one Workspace a WorkerClient prepared. It is
// opaque to the Remote backend; only the WorkerClient implementation gives
// it meaning.
type WorkerHandle string

// WorkerResult is the worker's finished work product for a prepared
// Workspace. A later ticket turns this into a Git bundle the controller
// imports; this ticket carries only the worker's Workspace, so a fetched
// result already reflects any changes the Agent or a Command made there.
type WorkerResult struct {
	Workspace domain.Workspace
}

// WorkerClient is the single controller-to-worker boundary the Remote
// backend drives: prepare a Workspace at a pinned commit, run a Command or
// the Agent there, heartbeat while work is in progress, fetch the finished
// result, and clean up. Keeping every worker operation behind one interface
// lets the transport stay swappable and fakeable for tests.
type WorkerClient interface {
	// PrepareWorkspace has the worker fetch the repository read-only at
	// req.Base and returns a handle for the prepared Workspace, plus the
	// Workspace itself as the worker sees it.
	PrepareWorkspace(ctx context.Context, req execution.WorkspaceRequest) (WorkerHandle, domain.Workspace, error)

	// Execute runs cmd on the worker, inside the Workspace handle
	// identifies.
	Execute(ctx context.Context, handle WorkerHandle, cmd execution.Command) (execution.Result, error)

	// RunAgent runs the Agent on the worker, inside the Workspace handle
	// identifies.
	RunAgent(ctx context.Context, handle WorkerHandle, req agent.AgentRequest) (agent.AgentResult, error)

	// Heartbeat reports the worker is still alive, so the controller can
	// keep the execution's lease from expiring.
	Heartbeat(ctx context.Context, handle WorkerHandle) error

	// FetchResult retrieves the worker's finished work product for handle.
	FetchResult(ctx context.Context, handle WorkerHandle) (WorkerResult, error)

	// Cleanup tears down the Workspace handle identifies.
	Cleanup(ctx context.Context, handle WorkerHandle) error
}

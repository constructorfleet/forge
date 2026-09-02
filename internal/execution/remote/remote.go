package remote

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/storage"
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
	worker            WorkerClient
	workerRef         string
	recover           RecoverFunc
	leases            LeaseStore
	heartbeatInterval time.Duration
	ttl               time.Duration
}

// NewBackend returns a Backend that drives worker through the WorkerClient
// seam. recover distinguishes a vanished worker (heartbeat lapse) from a
// worker-reported failure; pass nil to disable loss detection.
func NewBackend(worker WorkerClient, recover RecoverFunc) *Backend {
	return &Backend{worker: worker, recover: recover, heartbeatInterval: defaultHeartbeatInterval, ttl: defaultLeaseTTL}
}

// LeaseStore is the lease storage surface the Remote backend needs.
type LeaseStore interface {
	ClaimExecutionLease(ctx context.Context, executionID, issueID string, expiresAt time.Time) error
	HeartbeatExecutionLease(ctx context.Context, executionID, issueID string, expiresAt time.Time) error
	ReleaseExecutionLease(ctx context.Context, executionID, issueID string) error
	RecordExecutionPlacement(ctx context.Context, placement storage.ExecutionPlacement) error
}

const (
	defaultHeartbeatInterval = 10 * time.Second
	defaultLeaseTTL          = 30 * time.Second
)

// NewBackendWithLeases returns a Backend that claims an ExecutionLease for
// each prepared remote Workspace.
func NewBackendWithLeases(worker WorkerClient, recover RecoverFunc, leases LeaseStore) *Backend {
	b := NewBackend(worker, recover)
	b.leases = leases
	return b
}

// Prepare has the worker fetch the repository read-only at req.Base and
// returns an environment that drives every later operation on that worker.
func (b *Backend) Prepare(ctx context.Context, req execution.WorkspaceRequest) (execution.ExecutionEnvironment, error) {
	handle, ws, err := b.worker.PrepareWorkspace(ctx, req)
	if err != nil {
		return nil, err
	}
	if b.leases != nil {
		if err := b.leases.ClaimExecutionLease(ctx, req.ExecutionID, req.IssueID, time.Now().Add(b.ttl)); err != nil {
			_ = b.worker.Cleanup(ctx, handle)
			return nil, fmt.Errorf("remote: claim execution lease %s/%s: %w", req.ExecutionID, req.IssueID, err)
		}
		placement := storage.ExecutionPlacement{
			ExecutionID: req.ExecutionID,
			IssueID:     req.IssueID,
			Backend:     "remote",
			WorkerRef:   b.placementWorkerRef(handle),
			Workspace:   ws,
			Lifecycle:   domain.WorkspaceLifecycleActive,
		}
		if err := b.leases.RecordExecutionPlacement(ctx, placement); err != nil {
			_ = b.leases.ReleaseExecutionLease(ctx, req.ExecutionID, req.IssueID)
			_ = b.worker.Cleanup(ctx, handle)
			return nil, fmt.Errorf("remote: record execution placement %s/%s: %w", req.ExecutionID, req.IssueID, err)
		}
	}
	return &environment{
		worker:      b.worker,
		handle:      handle,
		workspace:   ws,
		executionID: req.ExecutionID,
		issueID:     req.IssueID,
		recover:     b.recover,
		leases:      b.leases,
		heartbeat:   b.heartbeatInterval,
		ttl:         b.ttl,
	}, nil
}

func (b *Backend) placementWorkerRef(handle WorkerHandle) string {
	if b.workerRef != "" {
		return b.workerRef
	}
	return string(handle)
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
	leases      LeaseStore
	heartbeat   time.Duration
	ttl         time.Duration
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
	recoverCtx := ctx
	ctx, finish := e.startHeartbeat(ctx)
	defer finish()

	result, err := e.worker.Execute(ctx, e.handle, cmd)
	if heartbeatErr := finish(); heartbeatErr != nil && err == nil {
		err = heartbeatErr
	}
	return result, classifyErr(recoverCtx, e.recover, e.executionID, e.issueID, err)
}

// Agent returns an agent.Agent that runs on the worker, inside the prepared
// Workspace.
func (e *environment) Agent() agent.Agent {
	return &remoteAgent{
		worker:      e.worker,
		handle:      e.handle,
		executionID: e.executionID,
		issueID:     e.issueID,
		recover:     e.recover,
		start:       e.startHeartbeat,
	}
}

// Cleanup tears down the worker's Workspace.
func (e *environment) Cleanup(ctx context.Context) error {
	if err := e.worker.Cleanup(ctx, e.handle); err != nil {
		return err
	}
	if e.leases == nil {
		return nil
	}
	if err := e.leases.ReleaseExecutionLease(ctx, e.executionID, e.issueID); err != nil {
		return fmt.Errorf("remote: release execution lease %s/%s: %w", e.executionID, e.issueID, err)
	}
	return nil
}

// remoteAgent adapts a WorkerClient's RunAgent call to the agent.Agent
// seam, so an environment can hand it out as a normal Agent.
type remoteAgent struct {
	worker      WorkerClient
	handle      WorkerHandle
	executionID string
	issueID     string
	recover     RecoverFunc
	start       func(context.Context) (context.Context, func() error)
}

// Execute runs req on the worker, inside the Workspace handle identifies.
// A WorkerClient transport error is classified against recover exactly as
// environment.Execute does: a lapsed lease wraps execution.ErrLost, an
// Agent-reported failure (agent.StatusFailed, no error) never reaches
// recover at all.
func (a *remoteAgent) Execute(ctx context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	if a.start != nil {
		recoverCtx := ctx
		var finish func() error
		ctx, finish = a.start(ctx)
		defer finish()

		result, err := a.worker.RunAgent(ctx, a.handle, req)
		if heartbeatErr := finish(); heartbeatErr != nil && err == nil {
			err = heartbeatErr
		}
		return result, classifyErr(recoverCtx, a.recover, a.executionID, a.issueID, err)
	}
	result, err := a.worker.RunAgent(ctx, a.handle, req)
	return result, classifyErr(ctx, a.recover, a.executionID, a.issueID, err)
}

func (e *environment) startHeartbeat(ctx context.Context) (context.Context, func() error) {
	if e.leases == nil || e.heartbeat <= 0 {
		return ctx, func() error { return nil }
	}

	heartbeatCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(e.heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				done <- nil
				return
			case <-ticker.C:
				if err := e.worker.Heartbeat(heartbeatCtx, e.handle); err != nil {
					cancel()
					done <- err
					return
				}
				if err := e.leases.HeartbeatExecutionLease(heartbeatCtx, e.executionID, e.issueID, time.Now().Add(e.ttl)); err != nil {
					cancel()
					done <- fmt.Errorf("remote: heartbeat execution lease %s/%s: %w", e.executionID, e.issueID, err)
					return
				}
			}
		}
	}()

	var once sync.Once
	var err error
	return heartbeatCtx, func() error {
		once.Do(func() {
			cancel()
			err = <-done
		})
		return err
	}
}

var (
	_ execution.ExecutionBackend     = (*Backend)(nil)
	_ execution.ExecutionEnvironment = (*environment)(nil)
	_ agent.Agent                    = (*remoteAgent)(nil)
)

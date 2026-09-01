package remote

import (
	"context"
	"errors"
	"sync"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/fake"
)

// errWorkerUnreachable is a sentinel PrepareWorkspace error tests can
// program to simulate a worker the controller cannot reach.
var errWorkerUnreachable = errors.New("remote: worker unreachable")

var _ WorkerClient = (*FakeWorker)(nil)

// FakeWorker is a deterministic in-memory WorkerClient for tests. It stands
// in for a real worker transport: PrepareWorkspace hands back the Workspace
// it was constructed with, Execute and RunAgent replay outcomes programmed
// per Command name or Issue ID (via the shared fake.OutcomeQueue), and
// every call is recorded for assertion.
type FakeWorker struct {
	workspace domain.Workspace

	prepareErr      error
	executeOutcomes *fake.OutcomeQueue[execution.Result]
	agentOutcomes   *fake.OutcomeQueue[agent.AgentResult]
	fetchResult     WorkerResult
	fetchErr        error

	mu         sync.Mutex
	prepared   []execution.WorkspaceRequest
	executed   []execution.Command
	agentCalls int
	heartbeats int
	fetched    int
	cleanedUp  []WorkerHandle
}

// NewFakeWorker returns a FakeWorker whose PrepareWorkspace and FetchResult
// calls return ws.
func NewFakeWorker(ws domain.Workspace) *FakeWorker {
	return &FakeWorker{
		workspace:       ws,
		executeOutcomes: fake.NewOutcomeQueue[execution.Result](),
		agentOutcomes:   fake.NewOutcomeQueue[agent.AgentResult](),
	}
}

// ProgramPrepareError makes PrepareWorkspace return err instead of a
// handle, simulating an unreachable or failing worker.
func (w *FakeWorker) ProgramPrepareError(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.prepareErr = err
}

// ProgramExecuteResult queues result as the next outcome Execute returns
// for the Command identified by name.
func (w *FakeWorker) ProgramExecuteResult(name string, result execution.Result) {
	w.executeOutcomes.ProgramResult(name, result)
}

// ProgramExecuteError queues err as the next outcome Execute returns for
// the Command identified by name.
func (w *FakeWorker) ProgramExecuteError(name string, err error) {
	w.executeOutcomes.ProgramError(name, err)
}

// ProgramAgentResult queues result as the next outcome RunAgent returns for
// the Issue identified by issueID.
func (w *FakeWorker) ProgramAgentResult(issueID string, result agent.AgentResult) {
	w.agentOutcomes.ProgramResult(issueID, result)
}

// ProgramAgentError queues err as the next outcome RunAgent returns for the
// Issue identified by issueID.
func (w *FakeWorker) ProgramAgentError(issueID string, err error) {
	w.agentOutcomes.ProgramError(issueID, err)
}

// ProgramFetchResult makes FetchResult return result.
func (w *FakeWorker) ProgramFetchResult(result WorkerResult) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fetchResult = result
	w.fetchErr = nil
}

// ProgramFetchError makes FetchResult return err instead of a result,
// simulating a worker whose finished work product cannot be retrieved.
func (w *FakeWorker) ProgramFetchError(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fetchErr = err
}

// PrepareWorkspace records req and returns the FakeWorker's Workspace, or
// the programmed error if ProgramPrepareError was called.
func (w *FakeWorker) PrepareWorkspace(_ context.Context, req execution.WorkspaceRequest) (WorkerHandle, domain.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.prepareErr != nil {
		return "", domain.Workspace{}, w.prepareErr
	}
	w.prepared = append(w.prepared, req)
	return WorkerHandle(req.ExecutionID + "/" + req.IssueID), w.workspace, nil
}

// Execute records cmd and returns the next programmed outcome for
// cmd.Name, per fake.OutcomeQueue's consume/repeat/default rules.
func (w *FakeWorker) Execute(_ context.Context, _ WorkerHandle, cmd execution.Command) (execution.Result, error) {
	w.mu.Lock()
	w.executed = append(w.executed, cmd)
	w.mu.Unlock()
	return w.executeOutcomes.Next(cmd.Name)
}

// RunAgent returns the next programmed outcome for req.Issue.ID, per
// fake.OutcomeQueue's consume/repeat/default rules.
func (w *FakeWorker) RunAgent(_ context.Context, _ WorkerHandle, req agent.AgentRequest) (agent.AgentResult, error) {
	w.mu.Lock()
	w.agentCalls++
	w.mu.Unlock()
	return w.agentOutcomes.Next(req.Issue.ID)
}

// Heartbeat records that it was called and always succeeds.
func (w *FakeWorker) Heartbeat(_ context.Context, _ WorkerHandle) error {
	w.mu.Lock()
	w.heartbeats++
	w.mu.Unlock()
	return nil
}

// FetchResult records that it was called and returns the programmed
// WorkerResult (via ProgramFetchResult), or the programmed error (via
// ProgramFetchError).
func (w *FakeWorker) FetchResult(_ context.Context, _ WorkerHandle) (WorkerResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fetched++
	if w.fetchErr != nil {
		return WorkerResult{}, w.fetchErr
	}
	return w.fetchResult, nil
}

// Cleanup records handle and always succeeds.
func (w *FakeWorker) Cleanup(_ context.Context, handle WorkerHandle) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cleanedUp = append(w.cleanedUp, handle)
	return nil
}

// Prepared returns every WorkspaceRequest passed to PrepareWorkspace so
// far, in call order.
func (w *FakeWorker) Prepared() []execution.WorkspaceRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]execution.WorkspaceRequest, len(w.prepared))
	copy(out, w.prepared)
	return out
}

// Executed returns every Command passed to Execute so far, in call order.
func (w *FakeWorker) Executed() []execution.Command {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]execution.Command, len(w.executed))
	copy(out, w.executed)
	return out
}

// AgentCalls returns how many times RunAgent was called.
func (w *FakeWorker) AgentCalls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.agentCalls
}

// Heartbeats returns how many times Heartbeat was called.
func (w *FakeWorker) Heartbeats() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.heartbeats
}

// FetchResultCalls returns how many times FetchResult was called.
func (w *FakeWorker) FetchResultCalls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fetched
}

// CleanedUp returns every WorkerHandle passed to Cleanup so far, in call
// order.
func (w *FakeWorker) CleanedUp() []WorkerHandle {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]WorkerHandle, len(w.cleanedUp))
	copy(out, w.cleanedUp)
	return out
}

package remote

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/storage"
)

func newTestBackend(ws domain.Workspace) (*Backend, *FakeWorker) {
	worker := NewFakeWorker(ws)
	return NewBackend(worker, nil), worker
}

func TestBackend_PreparePassesRequestToWorkerAndReturnsItsWorkspace(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42", Path: "/remote/issue-42", Branch: "forge/exec1/issue-42"}
	backend, worker := newTestBackend(ws)

	req := execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42", Base: "deadbeef"}
	env, err := backend.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if env.Workspace() != ws {
		t.Errorf("Workspace() = %+v, want %+v", env.Workspace(), ws)
	}

	prepared := worker.Prepared()
	if len(prepared) != 1 || prepared[0] != req {
		t.Errorf("worker.Prepared() = %+v, want [%+v]", prepared, req)
	}
}

func TestBackend_PrepareReportsWorkerFailure(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	backend, worker := newTestBackend(ws)
	worker.ProgramPrepareError(errWorkerUnreachable)

	_, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err == nil {
		t.Fatal("Prepare() error = nil, want the worker's error")
	}
}

func TestBackend_PrepareClaimsExecutionLeaseAndCleanupReleasesIt(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42", Path: "/remote/issue-42", Branch: "forge/exec1/issue-42"}
	leases := &fakeLeaseStore{}
	backend := NewBackendWithLeases(NewFakeWorker(ws), nil, leases)

	req := execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42", Base: "deadbeef"}
	env, err := backend.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if len(leases.claims) != 1 {
		t.Fatalf("lease claims = %+v, want one claim", leases.claims)
	}
	if leases.claims[0].executionID != "exec1" || leases.claims[0].issueID != "issue-42" {
		t.Errorf("lease claim = %+v, want exec1/issue-42", leases.claims[0])
	}
	if len(leases.placements) != 1 {
		t.Fatalf("placements = %+v, want one placement", leases.placements)
	}
	wantPlacement := storage.ExecutionPlacement{
		ExecutionID: "exec1",
		IssueID:     "issue-42",
		Backend:     "remote",
		WorkerRef:   "exec1/issue-42",
		Workspace:   ws,
		Lifecycle:   domain.WorkspaceLifecycleActive,
	}
	if leases.placements[0] != wantPlacement {
		t.Errorf("placement = %+v, want %+v", leases.placements[0], wantPlacement)
	}

	if err := env.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(leases.releases) != 1 {
		t.Fatalf("lease releases = %+v, want one release", leases.releases)
	}
	if leases.releases[0].executionID != "exec1" || leases.releases[0].issueID != "issue-42" {
		t.Errorf("lease release = %+v, want exec1/issue-42", leases.releases[0])
	}
}

func TestBackend_PrepareCleansUpWorkerWhenLeaseClaimFails(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	worker := NewFakeWorker(ws)
	leases := &fakeLeaseStore{claimErr: errTransport}
	backend := NewBackendWithLeases(worker, nil, leases)

	_, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err == nil {
		t.Fatal("Prepare: want lease claim error, got nil")
	}
	if calls := worker.CleanedUp(); len(calls) != 1 {
		t.Fatalf("worker.CleanedUp() = %+v, want one cleanup after lease claim failure", calls)
	}
}

func TestEnvironment_ExecuteRunsCommandOnWorker(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	backend, worker := newTestBackend(ws)
	worker.ProgramExecuteResult("build", execution.Result{Name: "build", ExitCode: 0, Stdout: "ok"})

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	result, err := env.Execute(context.Background(), execution.Command{Name: "build", Command: "make build"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 || result.Stdout != "ok" {
		t.Errorf("Execute() = %+v, want programmed result", result)
	}

	executed := worker.Executed()
	if len(executed) != 1 || executed[0].Name != "build" {
		t.Errorf("worker.Executed() = %+v, want one build command", executed)
	}
}

func TestEnvironment_ExecuteHeartbeatsLeaseWhileCommandRuns(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	worker := newBlockingWorker(ws)
	leases := &fakeLeaseStore{}
	backend := NewBackendWithLeases(worker, nil, leases)
	backend.heartbeatInterval = time.Millisecond
	backend.ttl = time.Second

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := env.Execute(context.Background(), execution.Command{Name: "slow", Command: "sleep"})
		done <- err
	}()
	worker.waitStarted(t)
	waitFor(t, func() bool {
		return worker.Heartbeats() > 0 && leases.HeartbeatCount() > 0
	}, "worker and lease heartbeats")
	worker.finish()

	if err := <-done; err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

func TestEnvironment_ExecuteHeartbeatFailureRoutesThroughRecovery(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	worker := newBlockingWorker(ws)
	worker.heartbeatErr = errTransport
	leases := &fakeLeaseStore{}
	backend := NewBackendWithLeases(worker, func(_ context.Context, executionID, issueID string) (bool, error) {
		if executionID != "exec1" || issueID != "issue-42" {
			t.Errorf("recover called with %s/%s, want exec1/issue-42", executionID, issueID)
		}
		return true, nil
	}, leases)
	backend.heartbeatInterval = time.Millisecond
	backend.ttl = time.Second

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	_, err = env.Execute(context.Background(), execution.Command{Name: "slow", Command: "sleep"})
	if !errors.Is(err, execution.ErrLost) {
		t.Fatalf("Execute error = %v, want it to wrap execution.ErrLost", err)
	}
}

func TestEnvironment_AgentRunsOnWorker(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	backend, worker := newTestBackend(ws)
	worker.ProgramAgentResult("issue-42", agent.AgentResult{Status: agent.StatusImplemented, Summary: "done"})

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	result, err := env.Agent().Execute(context.Background(), agent.AgentRequest{Issue: domain.Issue{ID: "issue-42"}})
	if err != nil {
		t.Fatalf("Agent().Execute: %v", err)
	}
	if result.Status != agent.StatusImplemented || result.Summary != "done" {
		t.Errorf("Agent().Execute() = %+v, want programmed result", result)
	}
}

func TestRemoteAgent_ExecuteHeartbeatsLeaseWhileAgentRuns(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	worker := newBlockingWorker(ws)
	leases := &fakeLeaseStore{}
	backend := NewBackendWithLeases(worker, nil, leases)
	backend.heartbeatInterval = time.Millisecond
	backend.ttl = time.Second

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := env.Agent().Execute(context.Background(), agent.AgentRequest{Issue: domain.Issue{ID: "issue-42"}})
		done <- err
	}()
	worker.waitStarted(t)
	waitFor(t, func() bool {
		return worker.Heartbeats() > 0 && leases.HeartbeatCount() > 0
	}, "worker and lease heartbeats")
	worker.finish()

	if err := <-done; err != nil {
		t.Fatalf("Agent().Execute: %v", err)
	}
}

func TestEnvironment_CleanupTearsDownWorkerWorkspace(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	backend, worker := newTestBackend(ws)

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if err := env.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if calls := worker.CleanedUp(); len(calls) != 1 {
		t.Errorf("worker.CleanedUp() = %+v, want one cleanup call", calls)
	}
}

var _ execution.ExecutionBackend = (*Backend)(nil)

type fakeLeaseStore struct {
	mu sync.Mutex

	claimErr error

	claims []struct {
		executionID string
		issueID     string
		expiresAt   time.Time
	}
	heartbeats []struct {
		executionID string
		issueID     string
		expiresAt   time.Time
	}
	releases []struct {
		executionID string
		issueID     string
	}
	placements []storage.ExecutionPlacement
}

func (s *fakeLeaseStore) ClaimExecutionLease(_ context.Context, executionID, issueID string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimErr != nil {
		return s.claimErr
	}
	s.claims = append(s.claims, struct {
		executionID string
		issueID     string
		expiresAt   time.Time
	}{executionID: executionID, issueID: issueID, expiresAt: expiresAt})
	return nil
}

func (s *fakeLeaseStore) HeartbeatExecutionLease(_ context.Context, executionID, issueID string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heartbeats = append(s.heartbeats, struct {
		executionID string
		issueID     string
		expiresAt   time.Time
	}{executionID: executionID, issueID: issueID, expiresAt: expiresAt})
	return nil
}

func (s *fakeLeaseStore) ReleaseExecutionLease(_ context.Context, executionID, issueID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases = append(s.releases, struct {
		executionID string
		issueID     string
	}{executionID: executionID, issueID: issueID})
	return nil
}

func (s *fakeLeaseStore) RecordExecutionPlacement(_ context.Context, placement storage.ExecutionPlacement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.placements = append(s.placements, placement)
	return nil
}

func (s *fakeLeaseStore) HeartbeatCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.heartbeats)
}

type blockingWorker struct {
	*FakeWorker

	started      chan struct{}
	release      chan struct{}
	heartbeatErr error
}

func newBlockingWorker(ws domain.Workspace) *blockingWorker {
	return &blockingWorker{
		FakeWorker: NewFakeWorker(ws),
		started:    make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (w *blockingWorker) Execute(ctx context.Context, _ WorkerHandle, cmd execution.Command) (execution.Result, error) {
	w.mu.Lock()
	w.executed = append(w.executed, cmd)
	w.mu.Unlock()
	close(w.started)
	select {
	case <-w.release:
		return execution.Result{Name: cmd.Name, ExitCode: 0}, nil
	case <-ctx.Done():
		return execution.Result{}, ctx.Err()
	}
}

func (w *blockingWorker) RunAgent(ctx context.Context, _ WorkerHandle, req agent.AgentRequest) (agent.AgentResult, error) {
	w.mu.Lock()
	w.agentCalls++
	w.mu.Unlock()
	close(w.started)
	select {
	case <-w.release:
		return agent.AgentResult{Status: agent.StatusImplemented, Summary: req.Issue.ID}, nil
	case <-ctx.Done():
		return agent.AgentResult{}, ctx.Err()
	}
}

func (w *blockingWorker) Heartbeat(ctx context.Context, handle WorkerHandle) error {
	if w.heartbeatErr != nil {
		return w.heartbeatErr
	}
	return w.FakeWorker.Heartbeat(ctx, handle)
}

func (w *blockingWorker) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-w.started:
	case <-time.After(time.Second):
		t.Fatal("worker command did not start")
	}
}

func (w *blockingWorker) finish() {
	close(w.release)
}

func waitFor(t *testing.T, condition func() bool, label string) {
	t.Helper()
	deadline := time.After(time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", label)
		case <-tick.C:
		}
	}
}

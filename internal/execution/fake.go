package execution

import (
	"context"
	"sync"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/fake"
)

// var declarations ensure FakeBackend and FakeEnvironment satisfy their
// interfaces at compile time.
var (
	_ ExecutionBackend     = (*FakeBackend)(nil)
	_ ExecutionEnvironment = (*FakeEnvironment)(nil)
)

// FakeBackend is a deterministic in-memory ExecutionBackend for tests.
// Outcomes are programmed per Issue ID via the shared fake.OutcomeQueue
// (consume in order, repeat the last one, fall back to a default); it also
// records every WorkspaceRequest passed to Prepare for later assertion.
type FakeBackend struct {
	outcomes *fake.OutcomeQueue[ExecutionEnvironment]

	mu          sync.Mutex
	invocations []WorkspaceRequest
}

// NewFakeBackend returns an empty FakeBackend with no programmed outcomes.
func NewFakeBackend() *FakeBackend {
	return &FakeBackend{outcomes: fake.NewOutcomeQueue[ExecutionEnvironment]()}
}

// ProgramResult queues env as the next outcome Prepare returns for the
// Issue identified by issueID.
func (b *FakeBackend) ProgramResult(issueID string, env ExecutionEnvironment) {
	b.outcomes.ProgramResult(issueID, env)
}

// ProgramError queues err as the next outcome Prepare returns for the
// Issue identified by issueID.
func (b *FakeBackend) ProgramError(issueID string, err error) {
	b.outcomes.ProgramError(issueID, err)
}

// ProgramDefault sets the outcome Prepare returns for any Issue with no
// (or exhausted) queued outcomes of its own.
func (b *FakeBackend) ProgramDefault(env ExecutionEnvironment) {
	b.outcomes.ProgramDefault(env)
}

// Prepare records req and returns the next programmed outcome for
// req.IssueID, per fake.OutcomeQueue's consume/repeat/default rules.
func (b *FakeBackend) Prepare(_ context.Context, req WorkspaceRequest) (ExecutionEnvironment, error) {
	b.mu.Lock()
	b.invocations = append(b.invocations, req)
	b.mu.Unlock()

	return b.outcomes.Next(req.IssueID)
}

// Invocations returns every WorkspaceRequest passed to Prepare so far, in
// call order.
func (b *FakeBackend) Invocations() []WorkspaceRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]WorkspaceRequest, len(b.invocations))
	copy(out, b.invocations)
	return out
}

// FakeEnvironment is a deterministic in-memory ExecutionEnvironment for
// tests. Execute outcomes are programmed per Command name via the shared
// fake.OutcomeQueue; Workspace, Agent, and Cleanup are otherwise plain
// recorded state.
type FakeEnvironment struct {
	workspace domain.Workspace
	agent     agent.Agent

	outcomes *fake.OutcomeQueue[Result]

	mu            sync.Mutex
	cleanupCalled bool
	executed      []Command
}

// NewFakeEnvironment returns a FakeEnvironment for ws with no programmed
// Agent (Agent() returns nil) and no programmed Execute outcomes.
func NewFakeEnvironment(ws domain.Workspace) *FakeEnvironment {
	return NewFakeEnvironmentWithAgent(ws, nil)
}

// NewFakeEnvironmentWithAgent returns a FakeEnvironment for ws whose
// Agent() returns ag.
func NewFakeEnvironmentWithAgent(ws domain.Workspace, ag agent.Agent) *FakeEnvironment {
	return &FakeEnvironment{
		workspace: ws,
		agent:     ag,
		outcomes:  fake.NewOutcomeQueue[Result](),
	}
}

// ProgramExecuteResult queues result as the next outcome Execute returns
// for the Command identified by name.
func (e *FakeEnvironment) ProgramExecuteResult(name string, result Result) {
	e.outcomes.ProgramResult(name, result)
}

// ProgramExecuteError queues err as the next outcome Execute returns for
// the Command identified by name.
func (e *FakeEnvironment) ProgramExecuteError(name string, err error) {
	e.outcomes.ProgramError(name, err)
}

// Workspace returns the Workspace this environment was constructed with.
func (e *FakeEnvironment) Workspace() domain.Workspace {
	return e.workspace
}

// Execute records cmd and returns the next programmed outcome for
// cmd.Name, per fake.OutcomeQueue's consume/repeat/default rules.
func (e *FakeEnvironment) Execute(_ context.Context, cmd Command) (Result, error) {
	e.mu.Lock()
	e.executed = append(e.executed, cmd)
	e.mu.Unlock()

	return e.outcomes.Next(cmd.Name)
}

// Executed returns every Command passed to Execute so far, in call order.
func (e *FakeEnvironment) Executed() []Command {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Command, len(e.executed))
	copy(out, e.executed)
	return out
}

// Agent returns the Agent this environment was constructed with, or nil.
func (e *FakeEnvironment) Agent() agent.Agent {
	return e.agent
}

// Cleanup records that it was called and always succeeds.
func (e *FakeEnvironment) Cleanup(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cleanupCalled = true
	return nil
}

// CleanupCalled reports whether Cleanup has been called.
func (e *FakeEnvironment) CleanupCalled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cleanupCalled
}

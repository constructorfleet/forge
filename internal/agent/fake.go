package agent

import (
	"context"
	"fmt"
	"sync"
)

// var declaration ensures FakeAgent satisfies Agent at compile time.
var _ Agent = (*FakeAgent)(nil)

// ScenarioKey identifies which programmed outcome a FakeAgent should return
// for a request. ScenarioForRequest is the default keying strategy.
type ScenarioKey string

// ScenarioForRequest derives the ScenarioKey a FakeAgent uses to look up a
// programmed outcome for req. Tests program outcomes against this same key.
func ScenarioForRequest(req AgentRequest) ScenarioKey {
	return ScenarioKey(req.Issue.ID)
}

// outcome is one programmed Execute response: either a result or an error.
type outcome struct {
	result AgentResult
	err    error
}

// FakeAgent is a deterministic Agent implementation for tests. Outcomes are
// programmed per ScenarioKey (by default, per Issue ID); each Execute call
// consumes the next queued outcome for its scenario (or repeats the last
// one, so repair-iteration tests can call Execute more times than were
// explicitly programmed) and is recorded for later assertion.
type FakeAgent struct {
	mu             sync.Mutex
	invocations    []AgentRequest
	outcomes       map[ScenarioKey][]outcome
	defaultOutcome *outcome
}

// NewFakeAgent returns an empty FakeAgent with no programmed outcomes.
func NewFakeAgent() *FakeAgent {
	return &FakeAgent{outcomes: map[ScenarioKey][]outcome{}}
}

// ProgramResult queues result as the next outcome Execute returns for key.
func (f *FakeAgent) ProgramResult(key ScenarioKey, result AgentResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes[key] = append(f.outcomes[key], outcome{result: result})
}

// ProgramError queues err as the next outcome Execute returns for key.
func (f *FakeAgent) ProgramError(key ScenarioKey, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes[key] = append(f.outcomes[key], outcome{err: err})
}

// ProgramDefault sets the outcome Execute returns for any scenario with no
// (or exhausted) queued outcomes of its own.
func (f *FakeAgent) ProgramDefault(result AgentResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defaultOutcome = &outcome{result: result}
}

// Execute records req and returns the next programmed outcome for its
// scenario (see ScenarioForRequest). If a scenario's queue has more than
// one outcome, each call consumes one until only the last remains, which is
// then repeated. If nothing is programmed for the scenario, the default
// outcome is used; if no default is set either, Execute returns an error.
func (f *FakeAgent) Execute(_ context.Context, req AgentRequest) (AgentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.invocations = append(f.invocations, req)

	key := ScenarioForRequest(req)
	queue := f.outcomes[key]
	var oc outcome
	switch {
	case len(queue) > 0:
		oc = queue[0]
		if len(queue) > 1 {
			f.outcomes[key] = queue[1:]
		}
	case f.defaultOutcome != nil:
		oc = *f.defaultOutcome
	default:
		return AgentResult{}, fmt.Errorf("agent: no outcome programmed for scenario %q", key)
	}
	return oc.result, oc.err
}

// Invocations returns every AgentRequest passed to Execute so far, in call
// order. The returned slice is a copy; mutating it does not affect the
// FakeAgent.
func (f *FakeAgent) Invocations() []AgentRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]AgentRequest, len(f.invocations))
	copy(out, f.invocations)
	return out
}

package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/Teagan42/forge/internal/domain"
)

// var declaration ensures FakeAgent satisfies Agent at compile time.
var _ Agent = (*FakeAgent)(nil)

// outcome is one programmed Execute response: either a result or an error.
type outcome struct {
	result AgentResult
	err    error
}

// FakeAgent is a deterministic Agent implementation for tests. Outcomes are
// programmed per Issue ID; each Execute call consumes the next queued
// outcome for its Issue (or repeats the last one, so repair-iteration tests
// can call Execute more times than were explicitly programmed) and is
// recorded for later assertion.
type FakeAgent struct {
	mu             sync.Mutex
	invocations    []AgentRequest
	outcomes       map[string][]outcome
	defaultOutcome *outcome
}

// NewFakeAgent returns an empty FakeAgent with no programmed outcomes.
func NewFakeAgent() *FakeAgent {
	return &FakeAgent{outcomes: map[string][]outcome{}}
}

// ProgramResult queues result as the next outcome Execute returns for the
// Issue identified by issueID.
func (f *FakeAgent) ProgramResult(issueID string, result AgentResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes[issueID] = append(f.outcomes[issueID], outcome{result: result})
}

// ProgramError queues err as the next outcome Execute returns for the Issue
// identified by issueID.
func (f *FakeAgent) ProgramError(issueID string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes[issueID] = append(f.outcomes[issueID], outcome{err: err})
}

// ProgramDefault sets the outcome Execute returns for any Issue with no (or
// exhausted) queued outcomes of its own.
func (f *FakeAgent) ProgramDefault(result AgentResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defaultOutcome = &outcome{result: result}
}

// Execute records req and returns the next programmed outcome for
// req.Issue.ID. If that Issue's queue has more than one outcome, each call
// consumes one until only the last remains, which is then repeated. If
// nothing is programmed for the Issue, the default outcome is used; if no
// default is set either, Execute returns an error.
func (f *FakeAgent) Execute(_ context.Context, req AgentRequest) (AgentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.invocations = append(f.invocations, cloneRequest(req))

	issueID := req.Issue.ID
	queue := f.outcomes[issueID]
	var oc outcome
	switch {
	case len(queue) > 0:
		oc = queue[0]
		if len(queue) > 1 {
			f.outcomes[issueID] = queue[1:]
		}
	case f.defaultOutcome != nil:
		oc = *f.defaultOutcome
	default:
		return AgentResult{}, fmt.Errorf("agent: no outcome programmed for issue %q", issueID)
	}
	return oc.result, oc.err
}

// Invocations returns every AgentRequest passed to Execute so far, in call
// order. Each returned AgentRequest (including its reference-typed fields,
// such as Feedback, Repository.QualityGates, and Issue.Dependencies) is
// independent of both the caller's original slices and any other snapshot
// returned by Invocations; mutating one has no effect on the FakeAgent or
// on other snapshots.
func (f *FakeAgent) Invocations() []AgentRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]AgentRequest, len(f.invocations))
	for i, req := range f.invocations {
		out[i] = cloneRequest(req)
	}
	return out
}

// cloneRequest returns a copy of req whose reference-typed fields
// (Feedback, Repository.QualityGates, Issue.Dependencies) have independent
// backing arrays, so neither the caller nor a recorded invocation can
// retroactively mutate the other through a shared slice.
func cloneRequest(req AgentRequest) AgentRequest {
	clone := req
	clone.Feedback = append([]Feedback(nil), req.Feedback...)
	clone.Repository.QualityGates = append([]string(nil), req.Repository.QualityGates...)
	clone.Issue.Dependencies = append([]domain.Dependency(nil), req.Issue.Dependencies...)
	return clone
}

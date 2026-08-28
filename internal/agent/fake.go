package agent

import (
	"context"
	"sync"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/fake"
)

// var declaration ensures FakeAgent satisfies Agent at compile time.
var _ Agent = (*FakeAgent)(nil)

// FakeAgent is a deterministic Agent implementation for tests. Outcomes are
// programmed per Issue ID via the shared fake.OutcomeQueue (consume in
// order, repeat the last one, fall back to a default — see its doc
// comment); FakeAgent itself only owns recording invocations for later
// assertion.
type FakeAgent struct {
	outcomes *fake.OutcomeQueue[AgentResult]

	mu          sync.Mutex
	invocations []AgentRequest
}

// NewFakeAgent returns an empty FakeAgent with no programmed outcomes.
func NewFakeAgent() *FakeAgent {
	return &FakeAgent{outcomes: fake.NewOutcomeQueue[AgentResult]()}
}

// ProgramResult queues result as the next outcome Execute returns for the
// Issue identified by issueID.
func (f *FakeAgent) ProgramResult(issueID string, result AgentResult) {
	f.outcomes.ProgramResult(issueID, result)
}

// ProgramError queues err as the next outcome Execute returns for the Issue
// identified by issueID.
func (f *FakeAgent) ProgramError(issueID string, err error) {
	f.outcomes.ProgramError(issueID, err)
}

// ProgramDefault sets the outcome Execute returns for any Issue with no (or
// exhausted) queued outcomes of its own.
func (f *FakeAgent) ProgramDefault(result AgentResult) {
	f.outcomes.ProgramDefault(result)
}

// Execute records req and returns the next programmed outcome for
// req.Issue.ID, per fake.OutcomeQueue's consume/repeat/default rules.
func (f *FakeAgent) Execute(_ context.Context, req AgentRequest) (AgentResult, error) {
	f.mu.Lock()
	f.invocations = append(f.invocations, cloneRequest(req))
	f.mu.Unlock()

	return f.outcomes.Next(req.Issue.ID)
}

// Invocations returns every AgentRequest passed to Execute so far, in call
// order. Each returned AgentRequest (including its reference-typed fields,
// such as Feedback, Repository.QualityGates, Repository.Languages,
// Repository.PackageManagers, and Issue.Dependencies) is independent of both
// the caller's original slices and any other snapshot returned by
// Invocations; mutating one has no effect on the FakeAgent or on other
// snapshots.
func (f *FakeAgent) Invocations() []AgentRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]AgentRequest, len(f.invocations))
	for i, req := range f.invocations {
		out[i] = cloneRequest(req)
	}
	return out
}

// cloneRequest returns a copy of req whose reference-typed fields (Feedback,
// Repository.QualityGates, Repository.Languages, Repository.PackageManagers,
// Issue.Dependencies) have independent backing arrays, so neither the caller
// nor a recorded invocation can retroactively mutate the other through a
// shared slice.
func cloneRequest(req AgentRequest) AgentRequest {
	clone := req
	clone.Feedback = append([]Feedback(nil), req.Feedback...)
	clone.Repository.QualityGates = append([]string(nil), req.Repository.QualityGates...)
	clone.Repository.Languages = append([]string(nil), req.Repository.Languages...)
	clone.Repository.PackageManagers = append([]string(nil), req.Repository.PackageManagers...)
	clone.Issue.Dependencies = append([]domain.Dependency(nil), req.Issue.Dependencies...)
	return clone
}

package review

import (
	"context"
	"sync"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/fake"
	"github.com/Teagan42/forge/internal/gate"
)

// var declaration ensures FakeReviewer satisfies Reviewer at compile time.
var _ Reviewer = (*FakeReviewer)(nil)

// FakeReviewer is a deterministic Reviewer implementation for tests,
// mirroring agent.FakeAgent: outcomes are programmed per Issue ID via the
// shared fake.OutcomeQueue (consume in order, repeat the last one, fall
// back to a default), and FakeReviewer itself only owns recording
// invocations for later assertion.
type FakeReviewer struct {
	outcomes *fake.OutcomeQueue[Result]

	mu          sync.Mutex
	invocations []Request
}

// NewFakeReviewer returns an empty FakeReviewer with no programmed
// outcomes.
func NewFakeReviewer() *FakeReviewer {
	return &FakeReviewer{outcomes: fake.NewOutcomeQueue[Result]()}
}

// ProgramResult queues result as the next outcome Review returns for the
// Issue identified by issueID.
func (f *FakeReviewer) ProgramResult(issueID string, result Result) {
	f.outcomes.ProgramResult(issueID, result)
}

// ProgramError queues err as the next outcome Review returns for the Issue
// identified by issueID.
func (f *FakeReviewer) ProgramError(issueID string, err error) {
	f.outcomes.ProgramError(issueID, err)
}

// ProgramDefault sets the outcome Review returns for any Issue with no (or
// exhausted) queued outcomes of its own.
func (f *FakeReviewer) ProgramDefault(result Result) {
	f.outcomes.ProgramDefault(result)
}

// Review records req and returns the next programmed outcome for
// req.Issue.ID, per fake.OutcomeQueue's consume/repeat/default rules.
func (f *FakeReviewer) Review(_ context.Context, req Request) (Result, error) {
	f.mu.Lock()
	f.invocations = append(f.invocations, cloneRequest(req))
	f.mu.Unlock()

	return f.outcomes.Next(req.Issue.ID)
}

// Invocations returns every Request passed to Review so far, in call order.
// Each returned Request (including its reference-typed fields) is
// independent of both the caller's original slices and any other snapshot
// returned by Invocations.
func (f *FakeReviewer) Invocations() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Request, len(f.invocations))
	for i, req := range f.invocations {
		out[i] = cloneRequest(req)
	}
	return out
}

// cloneRequest returns a copy of req whose reference-typed fields
// (Repository.QualityGates/Languages/PackageManagers, Issue.Dependencies,
// GateResults) have independent backing arrays, so neither the caller nor a
// recorded invocation can retroactively mutate the other through a shared
// slice.
func cloneRequest(req Request) Request {
	clone := req
	clone.Repository.QualityGates = append([]string(nil), req.Repository.QualityGates...)
	clone.Repository.Languages = append([]string(nil), req.Repository.Languages...)
	clone.Repository.PackageManagers = append([]string(nil), req.Repository.PackageManagers...)
	clone.Issue.Dependencies = append([]domain.Dependency(nil), req.Issue.Dependencies...)
	clone.GateResults = append([]gate.Result(nil), req.GateResults...)
	return clone
}

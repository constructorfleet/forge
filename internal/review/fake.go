package review

import (
	"context"
	"fmt"
	"sync"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/gate"
)

// var declaration ensures FakeReviewer satisfies Reviewer at compile time.
var _ Reviewer = (*FakeReviewer)(nil)

// outcome is one programmed Review response: either a result or an error.
type outcome struct {
	result Result
	err    error
}

// FakeReviewer is a deterministic Reviewer implementation for tests,
// mirroring agent.FakeAgent: outcomes are programmed per Issue ID, and each
// Review call consumes the next queued outcome for its Issue (or repeats
// the last one) and is recorded for later assertion.
type FakeReviewer struct {
	mu             sync.Mutex
	invocations    []Request
	outcomes       map[string][]outcome
	defaultOutcome *outcome
}

// NewFakeReviewer returns an empty FakeReviewer with no programmed
// outcomes.
func NewFakeReviewer() *FakeReviewer {
	return &FakeReviewer{outcomes: map[string][]outcome{}}
}

// ProgramResult queues result as the next outcome Review returns for the
// Issue identified by issueID.
func (f *FakeReviewer) ProgramResult(issueID string, result Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes[issueID] = append(f.outcomes[issueID], outcome{result: result})
}

// ProgramError queues err as the next outcome Review returns for the Issue
// identified by issueID.
func (f *FakeReviewer) ProgramError(issueID string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes[issueID] = append(f.outcomes[issueID], outcome{err: err})
}

// ProgramDefault sets the outcome Review returns for any Issue with no (or
// exhausted) queued outcomes of its own.
func (f *FakeReviewer) ProgramDefault(result Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defaultOutcome = &outcome{result: result}
}

// Review records req and returns the next programmed outcome for
// req.Issue.ID, per the same consume/repeat/default rules as
// agent.FakeAgent.Execute.
func (f *FakeReviewer) Review(_ context.Context, req Request) (Result, error) {
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
		return Result{}, fmt.Errorf("review: no outcome programmed for issue %q", issueID)
	}
	return oc.result, oc.err
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

// Package scheduler extends Forge's execution engine from single-issue to
// multi-issue (ticket 26, `forge execute 345 344 343`): it resolves the
// Dependency DAG across a set of Issues, computes the ready set as
// Dependencies are satisfied, and dispatches the existing per-issue
// Executor (internal/engine's Execute) concurrently up to a configured
// bound. See CONTEXT.md "Scheduler", "Execution", "Dependency".
//
// Scheduler treats the per-issue Execute as an opaque, already-correct unit
// of work (ticket 18's PENDING -> ... -> REVIEWING/FAILED/NEEDS_INFO
// pipeline, extended by ticket 20's Review stage) and owns only the
// question of *when* to run it for each Issue and what base revision to
// hand it. It depends solely on the narrow interfaces below — an
// IssueFetcher, an Executor, a DependencyResolver, and a BaseResolver — so
// it stays backend-agnostic and fully testable with fakes (see
// scheduler_test.go): no real GitHub, git, or SQLite is required to exercise
// the scheduling and concurrency logic itself.
//
// A note on "PR merged": ticket 22 (commit/PR creation) does not exist yet,
// so there is no real signal to check a prerequisite's PR merge status
// against. DependencyResolver is the seam that stands in for it — a fake in
// tests today, a real GitHub-merge-reachability check wired in by ticket 27
// tomorrow. Scheduler itself never special-cases "how" satisfaction is
// determined.
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

// defaultPollInterval is how often Run rechecks Dependency satisfaction for
// blocked Issues when nothing within the Execution has changed to wake it
// (e.g. all remaining Dependencies are External Issues whose satisfaction
// can only change via DependencyResolver, not via another dispatched
// Issue's completion). It bounds Run to a slow poll rather than a busy
// loop; issue completions and dispatch decisions wake Run immediately via
// an internal signal instead of waiting for this interval. Tests override
// it (via Scheduler.PollInterval) to keep runs fast.
const defaultPollInterval = 2 * time.Second

// IssueFetcher is the subset of tracker.Tracker Scheduler needs: fetching a
// normalized Issue (including its Dependencies) by ID. Mirrors
// engine.IssueFetcher's shape without importing internal/engine, keeping
// this package's core free of any engine dependency (see adapter.go for the
// one place that bridges to *engine.Engine).
type IssueFetcher interface {
	GetIssue(ctx context.Context, id string) (domain.Issue, error)
}

// Executor runs one Issue's full per-issue pipeline to a resting state —
// the schedulable unit this package wraps. *engine.Engine satisfies this
// via the Adapt function in adapter.go; tests inject a fake directly.
type Executor interface {
	Execute(ctx context.Context, issueID, baseRevision string) (domain.IssueState, error)
}

// DependencyResolver reports whether the Dependency of issueID on
// dependsOnID is satisfied (CONTEXT.md "Dependency": the prerequisite's PR
// merged into the applicable base — not local completion, not green CI).
// Injected so Scheduler's own logic stays testable with a fake and the real
// check (ticket 27) can be wired in later without touching this package.
type DependencyResolver interface {
	Satisfied(ctx context.Context, issueID, dependsOnID string) (bool, error)
}

// DependencyResolverFunc adapts a plain function to a DependencyResolver.
type DependencyResolverFunc func(ctx context.Context, issueID, dependsOnID string) (bool, error)

func (f DependencyResolverFunc) Satisfied(ctx context.Context, issueID, dependsOnID string) (bool, error) {
	return f(ctx, issueID, dependsOnID)
}

// BaseResolver resolves the base revision a given Issue's Worker should
// capture right now. Scheduler calls it immediately before dispatching each
// Issue — its own READY moment (CONTEXT.md "Execution": "individual
// Workers capture their own start base when they transition to READY").
// For an Issue with no unmet Dependencies this is typically the
// Execution's starting base; for a dependency-blocked Issue that has just
// become ready, it is the current tip of the applicable base branch, which
// by then contains the merged prerequisite code.
type BaseResolver interface {
	CurrentBase(ctx context.Context, issueID string) (string, error)
}

// BaseResolverFunc adapts a plain function to a BaseResolver.
type BaseResolverFunc func(ctx context.Context, issueID string) (string, error)

func (f BaseResolverFunc) CurrentBase(ctx context.Context, issueID string) (string, error) {
	return f(ctx, issueID)
}

// FixedBase returns a BaseResolver that always resolves to the same
// revision, regardless of Issue. Useful for tests and for a degenerate
// single-base wiring.
func FixedBase(revision string) BaseResolver {
	return BaseResolverFunc(func(context.Context, string) (string, error) {
		return revision, nil
	})
}

// Result is one Issue's outcome from a Scheduler.Run call: the final state
// Executor.Execute reported, or the error it (or base/dependency
// resolution) returned.
type Result struct {
	IssueID string
	State   domain.IssueState
	Err     error
}

// Scheduler computes ready work from the Dependency DAG, current Issue
// states, and a concurrency limit, then dispatches Executor for each Issue
// as it becomes ready (CONTEXT.md "Scheduler").
type Scheduler struct {
	Tracker  IssueFetcher
	Executor Executor
	Resolver DependencyResolver
	Base     BaseResolver

	// MaxParallel bounds how many Executor.Execute calls may run
	// concurrently. Values <= 0 are treated as 1.
	MaxParallel int

	// PollInterval is the fallback recheck rate used only when Run has
	// nothing else to wake it (see defaultPollInterval). New sets it to
	// defaultPollInterval; tests may lower it.
	PollInterval time.Duration

	// OnComplete, if set, is invoked (from the dispatching goroutine, not
	// serialized against other calls) every time a dispatched Issue's
	// Executor.Execute call returns, before Run acts on the result. Wiring
	// code uses this to feed a completion-driven DependencyResolver (see
	// cmd/forge); tests use it to drive a fake resolver's state without
	// polling or sleeping.
	OnComplete func(issueID string, state domain.IssueState, err error)
}

// New builds a Scheduler from its dependencies, applying defaults
// (MaxParallel floors at 1, PollInterval defaults to defaultPollInterval).
func New(trk IssueFetcher, exec Executor, resolver DependencyResolver, base BaseResolver, maxParallel int) *Scheduler {
	if maxParallel <= 0 {
		maxParallel = 1
	}
	return &Scheduler{
		Tracker:      trk,
		Executor:     exec,
		Resolver:     resolver,
		Base:         base,
		MaxParallel:  maxParallel,
		PollInterval: defaultPollInterval,
	}
}

// Run resolves the Dependency DAG across issueIDs (rejecting cycles before
// any work starts), then dispatches Executor.Execute for each Issue as its
// Dependencies become satisfied, bounded to MaxParallel concurrent
// dispatches. It blocks until every Issue has been dispatched and every
// dispatch has completed, returning one Result per requested Issue ID.
//
// Run's own dispatch loop is single-threaded (only it decides what to
// dispatch and marks an Issue dispatched before launching its goroutine),
// which is what prevents double-scheduling: no Issue's Executor.Execute is
// ever invoked twice for a given Run, even though a real prerequisite-merge
// signal could otherwise race dependency satisfaction against an
// in-flight dispatch decision. This is in addition to, not instead of, the
// atomic READY -> CLAIMED storage.Store.ClaimIssue guard each dispatched
// Execute performs internally.
func (s *Scheduler) Run(ctx context.Context, issueIDs []string) (map[string]Result, error) {
	if len(issueIDs) == 0 {
		return map[string]Result{}, nil
	}

	issues := make([]domain.Issue, 0, len(issueIDs))
	for _, id := range issueIDs {
		issue, err := s.Tracker.GetIssue(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("scheduler: fetch issue %s: %w", id, err)
		}
		issues = append(issues, issue)
	}

	// tracker.BuildDAG both detects cycles (returning early, before any
	// Issue is dispatched) and observes External Issues referenced by a
	// Dependency but not in issueIDs (CONTEXT.md "External Issue") — those
	// are never dispatched but their satisfaction still gates dependents.
	if _, err := tracker.BuildDAG(issues); err != nil {
		return nil, fmt.Errorf("scheduler: dependency graph: %w", err)
	}

	deps := make(map[string][]string, len(issues))
	for _, issue := range issues {
		for _, dep := range issue.Dependencies {
			deps[issue.ID] = append(deps[issue.ID], dep.DependsOnID)
		}
	}

	maxParallel := s.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 1
	}
	pollInterval := s.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	sem := make(chan struct{}, maxParallel)

	var (
		mu         sync.Mutex
		dispatched = make(map[string]bool, len(issueIDs))
		results    = make(map[string]Result, len(issueIDs))
	)
	wake := make(chan struct{}, 1)
	signal := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}

	var wg sync.WaitGroup
	var (
		errMu    sync.Mutex
		firstErr error
	)
	recordErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		defer errMu.Unlock()
		if firstErr == nil {
			firstErr = err
		}
	}

	remaining := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(issueIDs) - len(dispatched)
	}

	for remaining() > 0 {
		mu.Lock()
		var ready []string
		for _, id := range issueIDs {
			if dispatched[id] {
				continue
			}
			ready = append(ready, id)
		}
		mu.Unlock()

		dispatchedThisRound := false
		capacityExhausted := false
		for _, id := range ready {
			ok, err := s.dependenciesSatisfied(ctx, id, deps[id])
			if err != nil {
				return nil, fmt.Errorf("scheduler: check dependencies for issue %s: %w", id, err)
			}
			if !ok {
				continue
			}

			select {
			case sem <- struct{}{}:
			default:
				capacityExhausted = true
				continue
			}

			mu.Lock()
			dispatched[id] = true
			mu.Unlock()
			dispatchedThisRound = true

			wg.Add(1)
			go func(issueID string) {
				defer wg.Done()
				defer func() { <-sem; signal() }()

				base, err := s.Base.CurrentBase(ctx, issueID)
				if err != nil {
					err = fmt.Errorf("scheduler: resolve base for issue %s: %w", issueID, err)
					s.recordResult(&mu, results, issueID, "", err)
					if s.OnComplete != nil {
						s.OnComplete(issueID, "", err)
					}
					recordErr(err)
					return
				}

				state, execErr := s.Executor.Execute(ctx, issueID, base)
				s.recordResult(&mu, results, issueID, state, execErr)
				if s.OnComplete != nil {
					s.OnComplete(issueID, state, execErr)
				}
				recordErr(execErr)
			}(id)
		}

		if remaining() == 0 {
			break
		}
		// Nothing newly dispatched (either nothing was ready, or everything
		// ready was blocked on a full semaphore) means nothing will change
		// until either an in-flight dispatch completes (signal()) or the
		// poll interval elapses to recheck External/DependencyResolver-only
		// satisfaction — never a busy loop.
		if !dispatchedThisRound || capacityExhausted {
			select {
			case <-wake:
			case <-time.After(pollInterval):
			case <-ctx.Done():
				wg.Wait()
				recordErr(ctx.Err())
				return results, firstErrOrNil(&errMu, &firstErr)
			}
		}
	}

	wg.Wait()
	return results, firstErrOrNil(&errMu, &firstErr)
}

func firstErrOrNil(mu *sync.Mutex, errPtr *error) error {
	mu.Lock()
	defer mu.Unlock()
	return *errPtr
}

func (s *Scheduler) recordResult(mu *sync.Mutex, results map[string]Result, issueID string, state domain.IssueState, err error) {
	mu.Lock()
	defer mu.Unlock()
	results[issueID] = Result{IssueID: issueID, State: state, Err: err}
}

// dependenciesSatisfied reports whether every one of issueID's direct
// Dependencies is satisfied per DependencyResolver. An Issue with no
// Dependencies is trivially satisfied.
func (s *Scheduler) dependenciesSatisfied(ctx context.Context, issueID string, dependsOn []string) (bool, error) {
	for _, dep := range dependsOn {
		ok, err := s.Resolver.Satisfied(ctx, issueID, dep)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

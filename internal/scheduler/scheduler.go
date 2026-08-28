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
//
// A note on the Execution model: today each dispatched Issue runs through
// its own independent Engine.Execute call, which mints its own Execution
// row — so a single Run over several Issues currently produces one
// Execution per Issue rather than the single shared Execution CONTEXT.md
// describes. See docs/adr/0010-one-execution-per-run.md and follow-up
// ticket 26b.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

// defaultPollInterval is how often Run rechecks Dependency satisfaction for
// blocked Issues while at least one dispatch is still in flight, as a
// defensive fallback alongside the immediate wake signal each dispatch
// completion sends. It does not apply once nothing is in flight and
// nothing was dispatched this round — that condition is a genuine stall
// (see Run's no-progress detection) and Run stops immediately rather than
// continuing to poll. Tests override it (via Scheduler.PollInterval) to
// keep runs fast.
const defaultPollInterval = 2 * time.Second

// IssueFetcher is the subset of tracker.Tracker Scheduler needs: fetching a
// normalized Issue (including its Dependencies) by ID. Mirrors
// engine.IssueFetcher's shape without importing internal/engine, keeping
// this package's core free of any engine dependency (see adapter.go for the
// one place that bridges to *engine.Engine).
type IssueFetcher interface {
	GetIssue(ctx context.Context, id string) (domain.Issue, error)
}

// ExecuteOutcome is what Executor.Execute reports for one Issue: the
// Execution it ran under (see docs/adr/0010-one-execution-per-run.md for
// why that is, today, one Execution per Issue rather than one per Run) and
// the Issue's resulting state.
type ExecuteOutcome struct {
	ExecutionID string
	State       domain.IssueState
}

// Executor runs one Issue's full per-issue pipeline to a resting state —
// the schedulable unit this package wraps. *engine.Engine satisfies this
// via the Adapt function in adapter.go; tests inject a fake directly.
type Executor interface {
	Execute(ctx context.Context, issueID, baseRevision string) (ExecuteOutcome, error)
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

// Result is one Issue's outcome from a Scheduler.Run call: the Execution it
// ran under and final state Executor.Execute reported, or the error it (or
// base/dependency resolution, or an unsatisfiable Dependency — see Run) hit
// instead.
type Result struct {
	IssueID     string
	ExecutionID string
	State       domain.IssueState
	Err         error
}

// Scheduler computes ready work from the Dependency DAG, current Issue
// states, and a concurrency limit, then dispatches Executor for each Issue
// as it becomes ready (CONTEXT.md "Scheduler"). Construct one via New,
// which applies required defaults (MaxParallel floors at 1, PollInterval
// defaults to defaultPollInterval); Run trusts those fields as already
// valid rather than re-defaulting them itself.
type Scheduler struct {
	Tracker  IssueFetcher
	Executor Executor
	Resolver DependencyResolver
	Base     BaseResolver

	// MaxParallel bounds how many Executor.Execute calls may run
	// concurrently. Set via New; must be >= 1.
	MaxParallel int

	// PollInterval is the fallback recheck rate used only while at least
	// one dispatch is in flight (see defaultPollInterval). Set via New;
	// tests may lower it. Must be positive.
	PollInterval time.Duration

	// OnComplete, if set, is invoked every time a dispatched Issue's
	// Executor.Execute call returns, after its Result is recorded but
	// before the corresponding Worker's semaphore slot is released. Run
	// holds its own internal lock for the duration of this call, so no
	// other Result can be recorded and no no-progress (stall) check can
	// observe this Issue as "finished" until OnComplete itself returns —
	// this is what lets wiring code (see cmd/forge) update a
	// completion-driven DependencyResolver's state from inside OnComplete
	// and have Run's very next readiness check already see it, with no
	// window where a dependent could be mistaken for permanently
	// unsatisfiable. OnComplete must not call back into this Scheduler
	// (e.g. Run) and should do only quick, non-blocking work, since it runs
	// serialized against every other dispatched Issue's completion.
	OnComplete func(issueID string, state domain.IssueState, err error)
}

// New builds a Scheduler from its dependencies, applying required defaults
// (MaxParallel floors at 1, PollInterval defaults to defaultPollInterval).
// Constructing a Scheduler any other way (a bare Scheduler{} literal) skips
// these defaults and is unsupported.
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
// dispatches. It returns one Result per requested Issue ID, alongside an
// error if any occurred; the Result map is populated (even if partially)
// for every code path, including the error ones, so callers always have
// whatever was learned before returning.
//
// Run stops itself in three situations, in addition to the everything-
// -dispatched-and-finished happy path:
//
//  1. A cycle in the Dependency DAG: detected up front, nothing is
//     dispatched at all.
//  2. DependencyResolver.Satisfied errors for some still-undispatched
//     Issue (e.g. a Dependency outside the requested set, which
//     cmd/forge's resolver treats as unsupported rather than hanging): Run
//     cancels its internal context, waits for every already-dispatched
//     Issue to finish, and returns their results alongside the error —
//     no goroutine is abandoned.
//  3. No progress is possible: an iteration where nothing new was
//     dispatched and nothing is currently in flight means no future
//     event can ever change that (there is nothing left running to
//     eventually satisfy a Dependency or free a semaphore slot), most
//     commonly because a prerequisite finished in a state
//     DependencyResolver will never consider satisfied (FAILED,
//     NEEDS_INFO, or a hard Executor error). Every still-undispatched
//     Issue is recorded with an explicit "unsatisfiable dependency"
//     error naming the specific Dependency that blocked it, and Run
//     returns immediately rather than polling forever.
//
// Run's own dispatch loop is single-threaded (only it decides what to
// dispatch and marks an Issue dispatched before launching its goroutine),
// which is what prevents double-scheduling: no Issue's Executor.Execute is
// ever invoked twice for a given Run, even though a real prerequisite-merge
// signal could otherwise race dependency satisfaction against an in-flight
// dispatch decision. This is in addition to, not instead of, the atomic
// READY -> CLAIMED storage.Store.ClaimIssue guard each dispatched Execute
// performs internally.
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
	dag, err := tracker.BuildDAG(issues)
	if err != nil {
		return nil, fmt.Errorf("scheduler: dependency graph: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, s.MaxParallel)

	var (
		mu         sync.Mutex
		dispatched = make(map[string]bool, len(issueIDs))
		results    = make(map[string]Result, len(issueIDs))
		blockedOn  = make(map[string]string, len(issueIDs)) // issueID -> unsatisfied dependsOnID, refreshed each round
		firstErr   error
	)

	wake := make(chan struct{}, 1)
	signal := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	recordErr := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if firstErr == nil {
			firstErr = err
		}
	}
	recordResult := func(issueID string, outcome ExecuteOutcome, err error) {
		mu.Lock()
		defer mu.Unlock()
		results[issueID] = Result{IssueID: issueID, ExecutionID: outcome.ExecutionID, State: outcome.State, Err: err}
		// OnComplete runs while still holding mu (see its doc comment): this
		// makes "Result recorded" and "OnComplete's side effects visible"
		// atomic from the main dispatch loop's point of view, so a
		// no-progress check can never observe this Issue as finished before
		// e.g. a completion-driven DependencyResolver has learned about it.
		if s.OnComplete != nil {
			s.OnComplete(issueID, outcome.State, err)
		}
	}
	snapshotResults := func() map[string]Result {
		mu.Lock()
		defer mu.Unlock()
		out := make(map[string]Result, len(results))
		for k, v := range results {
			out[k] = v
		}
		return out
	}
	remaining := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(issueIDs) - len(dispatched)
	}

	var wg sync.WaitGroup

	// runWorker is the Worker unit Run dispatches: resolve this Issue's
	// current base, run Executor.Execute, and record the outcome. Named to
	// match CONTEXT.md's "Worker" term (see also ticket 30's per-Worker
	// locks, a natural extension of this function).
	runWorker := func(issueID string) {
		defer wg.Done()
		defer func() { <-sem; signal() }()

		base, err := s.Base.CurrentBase(ctx, issueID)
		if err != nil {
			err = fmt.Errorf("scheduler: resolve base for issue %s: %w", issueID, err)
			recordResult(issueID, ExecuteOutcome{}, err)
			recordErr(err)
			return
		}

		outcome, execErr := s.Executor.Execute(ctx, issueID, base)
		recordResult(issueID, outcome, execErr)
		recordErr(execErr)
	}

	for remaining() > 0 {
		mu.Lock()
		var ready []string
		for _, id := range issueIDs {
			if !dispatched[id] {
				ready = append(ready, id)
			}
		}
		mu.Unlock()

		dispatchedThisRound := false
		capacityExhausted := false
		for _, id := range ready {
			dep, err := s.firstUnsatisfied(ctx, id, dag.DependsOn(id))
			if err != nil {
				cancel()
				wg.Wait()
				return snapshotResults(), fmt.Errorf("scheduler: check dependencies for issue %s: %w", id, err)
			}
			if dep != "" {
				mu.Lock()
				blockedOn[id] = dep
				mu.Unlock()
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
			go runWorker(id)
		}

		if remaining() == 0 {
			break
		}

		mu.Lock()
		inFlight := len(dispatched) - len(results)
		if !dispatchedThisRound && inFlight == 0 {
			// No-progress: nothing new was dispatched and nothing is
			// running, so no future signal can ever arrive (a completing
			// Worker, freeing a semaphore slot) to change that. Every
			// still-undispatched Issue is permanently blocked — most
			// commonly because its prerequisite finished in a state
			// DependencyResolver will never consider satisfied.
			var stallErrs []error
			for _, id := range issueIDs {
				if dispatched[id] {
					continue
				}
				dep := blockedOn[id]
				stallErr := fmt.Errorf(
					"scheduler: issue %s has an unsatisfiable dependency on %s: no further progress is possible",
					id, dep)
				dispatched[id] = true
				results[id] = Result{IssueID: id, Err: stallErr}
				stallErrs = append(stallErrs, stallErr)
			}
			out := make(map[string]Result, len(results))
			for k, v := range results {
				out[k] = v
			}
			joined := errors.Join(append([]error{firstErr}, stallErrs...)...)
			mu.Unlock()
			return out, joined
		}
		mu.Unlock()

		if !dispatchedThisRound || capacityExhausted {
			// Nothing newly dispatched (either nothing was ready, or
			// everything ready was blocked on a full semaphore) means
			// nothing will change until either an in-flight dispatch
			// completes (signal()) or the poll interval elapses to
			// recheck DependencyResolver-only satisfaction — never a
			// busy loop, and never reached with nothing in flight (that
			// case returns above instead).
			select {
			case <-wake:
			case <-time.After(s.PollInterval):
			case <-ctx.Done():
				wg.Wait()
				recordErr(ctx.Err())
				mu.Lock()
				err := firstErr
				mu.Unlock()
				return snapshotResults(), err
			}
		}
	}

	wg.Wait()
	mu.Lock()
	err = firstErr
	mu.Unlock()
	return snapshotResults(), err
}

// firstUnsatisfied returns the first of issueID's Dependencies (by
// dependsOnID) that DependencyResolver does not yet report satisfied, or ""
// if all are satisfied (including trivially, for an Issue with none).
func (s *Scheduler) firstUnsatisfied(ctx context.Context, issueID string, dependsOn []string) (dep string, err error) {
	for _, d := range dependsOn {
		ok, err := s.Resolver.Satisfied(ctx, issueID, d)
		if err != nil {
			return d, err
		}
		if !ok {
			return d, nil
		}
	}
	return "", nil
}

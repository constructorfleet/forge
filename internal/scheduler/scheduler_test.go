package scheduler_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/scheduler"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/workspace"
)

// stubTracker is a minimal scheduler.IssueFetcher double.
type stubTracker struct {
	issues map[string]domain.Issue
}

func (s *stubTracker) GetIssue(_ context.Context, id string) (domain.Issue, error) {
	issue, ok := s.issues[id]
	if !ok {
		return domain.Issue{}, errors.New("stubTracker: no issue " + id)
	}
	return issue, nil
}

var _ scheduler.IssueFetcher = (*stubTracker)(nil)

// alwaysSatisfied is a DependencyResolver reporting every Dependency
// satisfied immediately, for tests exercising concurrency rather than
// dependency gating.
var alwaysSatisfied = scheduler.DependencyResolverFunc(func(context.Context, string, string) (bool, error) {
	return true, nil
})

// gatedExecutor is a fake Executor that lets tests observe concurrency
// deterministically: each call signals onDispatch (while counted as
// "running") and then blocks until the test sends a token on release,
// rather than sleeping.
type gatedExecutor struct {
	mu          sync.Mutex
	running     int
	maxObserved int
	calls       map[string]int
	order       []string
	release     chan struct{}
	onDispatch  func(issueID string)
}

func newGatedExecutor() *gatedExecutor {
	return &gatedExecutor{calls: map[string]int{}, release: make(chan struct{})}
}

func (e *gatedExecutor) Execute(ctx context.Context, issueID, _ string) (scheduler.ExecuteOutcome, error) {
	e.mu.Lock()
	e.running++
	if e.running > e.maxObserved {
		e.maxObserved = e.running
	}
	e.calls[issueID]++
	e.order = append(e.order, issueID)
	e.mu.Unlock()

	if e.onDispatch != nil {
		e.onDispatch(issueID)
	}

	select {
	case <-e.release:
	case <-ctx.Done():
	}

	e.mu.Lock()
	e.running--
	e.mu.Unlock()
	return scheduler.ExecuteOutcome{ExecutionID: "exec-" + issueID, State: domain.StateReviewing}, nil
}

func (e *gatedExecutor) releaseOne() { e.release <- struct{}{} }

func (e *gatedExecutor) MaxObserved() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.maxObserved
}

func (e *gatedExecutor) CallCount(issueID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[issueID]
}

var _ scheduler.Executor = (*gatedExecutor)(nil)

func issueSet(ids ...string) map[string]domain.Issue {
	issues := make(map[string]domain.Issue, len(ids))
	for _, id := range ids {
		issues[id] = domain.Issue{ID: id}
	}
	return issues
}

// TestRun_IndependentIssuesExecuteConcurrently is the ticket's "independent
// issues run concurrently" integration criterion: three Issues with no
// Dependencies, max_parallel 3, must all be in flight simultaneously.
func TestRun_IndependentIssuesExecuteConcurrently(t *testing.T) {
	exec := newGatedExecutor()
	started := make(chan string, 3)
	exec.onDispatch = func(id string) { started <- id }

	sch := scheduler.New(&stubTracker{issues: issueSet("a", "b", "c")}, exec, alwaysSatisfied, scheduler.FixedBase("base"), 3)
	sch.PollInterval = time.Millisecond

	done := make(chan map[string]scheduler.Result, 1)
	errCh := make(chan error, 1)
	go func() {
		results, err := sch.Run(context.Background(), []string{"a", "b", "c"})
		errCh <- err
		done <- results
	}()

	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for issue %d to start", i+1)
		}
	}
	if got := exec.MaxObserved(); got != 3 {
		t.Fatalf("MaxObserved = %d, want 3 (independent issues did not overlap)", got)
	}

	exec.releaseOne()
	exec.releaseOne()
	exec.releaseOne()

	if err := <-errCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
	results := <-done
	for _, id := range []string{"a", "b", "c"} {
		if results[id].State != domain.StateReviewing {
			t.Errorf("results[%s].State = %s, want REVIEWING", id, results[id].State)
		}
		if exec.CallCount(id) != 1 {
			t.Errorf("CallCount(%s) = %d, want exactly 1 (no double-scheduling)", id, exec.CallCount(id))
		}
	}
}

// TestRun_MaxParallelRespected is the ticket's "max parallel respected"
// integration criterion: five independent Issues, max_parallel 2, must
// never have more than two Executor.Execute calls in flight at once.
func TestRun_MaxParallelRespected(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e"}
	exec := newGatedExecutor()
	started := make(chan string, len(ids))
	exec.onDispatch = func(id string) { started <- id }

	sch := scheduler.New(&stubTracker{issues: issueSet(ids...)}, exec, alwaysSatisfied, scheduler.FixedBase("base"), 2)
	sch.PollInterval = time.Millisecond

	done := make(chan map[string]scheduler.Result, 1)
	errCh := make(chan error, 1)
	go func() {
		results, err := sch.Run(context.Background(), ids)
		errCh <- err
		done <- results
	}()

	// Exactly two dispatch immediately; capacity is exhausted for the rest.
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for initial dispatch %d", i+1)
		}
	}
	select {
	case id := <-started:
		t.Fatalf("issue %s started before a slot freed, want max_parallel=2 respected", id)
	default:
	}
	if got := exec.MaxObserved(); got != 2 {
		t.Fatalf("MaxObserved = %d, want 2", got)
	}

	// Releasing one at a time must never let more than 2 run concurrently;
	// the third new dispatch happens for each of the first 3 releases (5
	// issues total, 2 already started), the last 2 releases just drain.
	newStarts := 0
	for i := 0; i < len(ids); i++ {
		exec.releaseOne()
		if newStarts < len(ids)-2 {
			select {
			case <-started:
				newStarts++
			case <-time.After(5 * time.Second):
				t.Fatalf("timed out waiting for dispatch after release %d", i+1)
			}
		}
	}
	if got := exec.MaxObserved(); got != 2 {
		t.Fatalf("MaxObserved = %d, want 2 (max_parallel bound violated at some point)", got)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
	results := <-done
	for _, id := range ids {
		if results[id].State != domain.StateReviewing {
			t.Errorf("results[%s].State = %s, want REVIEWING", id, results[id].State)
		}
		if exec.CallCount(id) != 1 {
			t.Errorf("CallCount(%s) = %d, want exactly 1", id, exec.CallCount(id))
		}
	}
}

// recordingExecutor records call order and the base revision each Issue was
// dispatched with, without blocking.
type recordingExecutor struct {
	mu    sync.Mutex
	order []string
	bases map[string]string
}

func (e *recordingExecutor) Execute(_ context.Context, issueID, base string) (scheduler.ExecuteOutcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.order = append(e.order, issueID)
	if e.bases == nil {
		e.bases = map[string]string{}
	}
	e.bases[issueID] = base
	return scheduler.ExecuteOutcome{ExecutionID: "exec-" + issueID, State: domain.StateReviewing}, nil
}

func (e *recordingExecutor) Order() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.order...)
}

func (e *recordingExecutor) BaseFor(issueID string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.bases[issueID]
}

// TestRun_DependentIssueWaitsThenUsesUpdatedBase is the ticket's "dependent
// issue waits for prerequisite, then starts from updated base" integration
// criterion, exercised purely against Scheduler's own logic via fakes: "b"
// depends on "a" and must not dispatch until the (fake) DependencyResolver
// reports "a" satisfied — which OnComplete flips only once "a" has actually
// finished — and must then be dispatched with the base the (fake)
// BaseResolver now reports for "b", not the base "a" started from.
func TestRun_DependentIssueWaitsThenUsesUpdatedBase(t *testing.T) {
	issues := issueSet("a", "b")
	issues["b"] = domain.Issue{ID: "b", Dependencies: []domain.Dependency{{IssueID: "b", DependsOnID: "a"}}}

	var mu sync.Mutex
	satisfied := map[string]bool{}
	baseFor := map[string]string{"a": "base-initial", "b": "base-initial"}

	resolver := scheduler.DependencyResolverFunc(func(_ context.Context, _, dependsOnID string) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		return satisfied[dependsOnID], nil
	})
	baseResolver := scheduler.BaseResolverFunc(func(_ context.Context, issueID string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		return baseFor[issueID], nil
	})

	exec := &recordingExecutor{}
	sch := scheduler.New(&stubTracker{issues: issues}, exec, resolver, baseResolver, 2)
	sch.PollInterval = 2 * time.Millisecond
	sch.OnComplete = func(issueID string, _ domain.IssueState, err error) {
		if issueID != "a" || err != nil {
			return
		}
		mu.Lock()
		satisfied["a"] = true
		baseFor["b"] = "base-after-merge"
		mu.Unlock()
	}

	results, err := sch.Run(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range []string{"a", "b"} {
		if results[id].State != domain.StateReviewing {
			t.Errorf("results[%s].State = %s, want REVIEWING", id, results[id].State)
		}
	}

	order := exec.Order()
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("dispatch order = %v, want [a b] (b must wait for a)", order)
	}
	if got := exec.BaseFor("a"); got != "base-initial" {
		t.Errorf("BaseFor(a) = %s, want base-initial", got)
	}
	if got := exec.BaseFor("b"); got != "base-after-merge" {
		t.Errorf("BaseFor(b) = %s, want base-after-merge (captured at b's READY moment, after a's merge)", got)
	}
}

// scriptedExecutor is a fake Executor that returns a fixed, programmed
// ExecuteOutcome/error per Issue ID, without blocking. Used for the no-
// -progress (stall) tests below where the prerequisite's own Execute must
// return promptly in a non-publish-ready state.
type scriptedExecutor struct {
	mu       sync.Mutex
	outcomes map[string]scheduler.ExecuteOutcome
	errs     map[string]error
	calls    map[string]int
}

func (e *scriptedExecutor) Execute(_ context.Context, issueID, _ string) (scheduler.ExecuteOutcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.calls == nil {
		e.calls = map[string]int{}
	}
	e.calls[issueID]++
	return e.outcomes[issueID], e.errs[issueID]
}

func (e *scriptedExecutor) CallCount(issueID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[issueID]
}

var _ scheduler.Executor = (*scriptedExecutor)(nil)

// TestRun_PrerequisiteFailed_DependentRecordedUnsatisfiable is the P1
// no-progress fix's core regression test: "a" finishes FAILED (a legal,
// non-error Executor outcome), which a realistic DependencyResolver (like
// cmd/forge's completionResolver) never considers satisfied. "b" (which
// depends on "a") must never be dispatched, and Run must return promptly
// with an explicit "unsatisfiable dependency" Result for "b" instead of
// polling forever.
func TestRun_PrerequisiteFailed_DependentRecordedUnsatisfiable(t *testing.T) {
	issues := issueSet("a", "b")
	issues["b"] = domain.Issue{ID: "b", Dependencies: []domain.Dependency{{IssueID: "b", DependsOnID: "a"}}}

	exec := &scriptedExecutor{outcomes: map[string]scheduler.ExecuteOutcome{
		"a": {ExecutionID: "exec-a", State: domain.StateFailed},
		"b": {ExecutionID: "exec-b", State: domain.StateReviewing},
	}}

	// A resolver that only ever considers "a" satisfied once it locally
	// reports success — mirroring cmd/forge's completionResolver, which
	// never marks a FAILED prerequisite satisfied.
	var mu sync.Mutex
	satisfied := map[string]bool{}
	resolver := scheduler.DependencyResolverFunc(func(_ context.Context, _, dependsOnID string) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		return satisfied[dependsOnID], nil
	})

	sch := scheduler.New(&stubTracker{issues: issues}, exec, resolver, scheduler.FixedBase("base"), 2)
	sch.PollInterval = 2 * time.Millisecond
	sch.OnComplete = func(issueID string, state domain.IssueState, err error) {
		if err != nil || state != domain.StateReviewing {
			return // FAILED never satisfies a Dependency.
		}
		mu.Lock()
		satisfied[issueID] = true
		mu.Unlock()
	}

	done := make(chan struct{})
	var results map[string]scheduler.Result
	var runErr error
	go func() {
		results, runErr = sch.Run(context.Background(), []string{"a", "b"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return promptly after the prerequisite failed (hang)")
	}

	if runErr == nil {
		t.Fatal("Run: want a non-nil error, got nil")
	}
	if results["a"].State != domain.StateFailed {
		t.Errorf("results[a].State = %s, want FAILED", results["a"].State)
	}
	if results["b"].Err == nil || !strings.Contains(results["b"].Err.Error(), "unsatisfiable") {
		t.Errorf("results[b].Err = %v, want an unsatisfiable-dependency error", results["b"].Err)
	}
	if results["b"].Err == nil || !strings.Contains(results["b"].Err.Error(), "a") {
		t.Errorf("results[b].Err = %v, want it to name the unsatisfied prerequisite %q", results["b"].Err, "a")
	}
	if got := exec.CallCount("b"); got != 0 {
		t.Errorf("CallCount(b) = %d, want 0 (b must never be dispatched)", got)
	}
}

// TestRun_PrerequisiteHardErrors_DependentRecordedUnsatisfiable is the P1
// fix's other required case: the prerequisite's Executor.Execute call
// itself returns a hard error (rather than merely finishing in a
// non-publish-ready state). The dependent must still be recorded
// unsatisfiable and Run must still return promptly.
func TestRun_PrerequisiteHardErrors_DependentRecordedUnsatisfiable(t *testing.T) {
	issues := issueSet("a", "b")
	issues["b"] = domain.Issue{ID: "b", Dependencies: []domain.Dependency{{IssueID: "b", DependsOnID: "a"}}}

	exec := &scriptedExecutor{
		outcomes: map[string]scheduler.ExecuteOutcome{"b": {State: domain.StateReviewing}},
		errs:     map[string]error{"a": errors.New("boom: agent subprocess crashed")},
	}
	resolver := alwaysUnsatisfied

	sch := scheduler.New(&stubTracker{issues: issues}, exec, resolver, scheduler.FixedBase("base"), 2)
	sch.PollInterval = 2 * time.Millisecond

	done := make(chan struct{})
	var results map[string]scheduler.Result
	var runErr error
	go func() {
		results, runErr = sch.Run(context.Background(), []string{"a", "b"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return promptly after the prerequisite hard-errored (hang)")
	}

	if runErr == nil {
		t.Fatal("Run: want a non-nil error, got nil")
	}
	if results["a"].Err == nil {
		t.Error("results[a].Err is nil, want the hard error Executor returned")
	}
	if results["b"].Err == nil || !strings.Contains(results["b"].Err.Error(), "unsatisfiable") {
		t.Errorf("results[b].Err = %v, want an unsatisfiable-dependency error", results["b"].Err)
	}
	if got := exec.CallCount("b"); got != 0 {
		t.Errorf("CallCount(b) = %d, want 0 (b must never be dispatched)", got)
	}
}

// alwaysUnsatisfied is a DependencyResolver reporting every Dependency
// unsatisfied, for the hard-error stall test above where satisfaction is
// never expected to flip.
var alwaysUnsatisfied = scheduler.DependencyResolverFunc(func(context.Context, string, string) (bool, error) {
	return false, nil
})

// TestRun_DependencyCheckError_CancelsAndWaitsForInFlightWorkers is the P2
// fix's regression test: while "a" is still mid-Execute (in flight),
// DependencyResolver.Satisfied errors for an unrelated Issue "c" (the
// out-of-set-dependency case cmd/forge's completionResolver hits). Run must
// not abandon "a": it cancels its internal context, waits for "a" to
// finish, and returns "a"'s Result alongside the error rather than leaking
// the goroutine or hanging.
func TestRun_DependencyCheckError_CancelsAndWaitsForInFlightWorkers(t *testing.T) {
	issues := issueSet("a", "c")
	issues["c"] = domain.Issue{ID: "c", Dependencies: []domain.Dependency{{IssueID: "c", DependsOnID: "x"}}}

	exec := newGatedExecutor()
	started := make(chan string, 1)
	exec.onDispatch = func(id string) { started <- id }

	wantErrSubstring := "outside the requested set"
	resolver := scheduler.DependencyResolverFunc(func(_ context.Context, issueID, dependsOnID string) (bool, error) {
		if issueID == "c" {
			return false, fmt.Errorf("issue c depends on %s, which is %s", dependsOnID, wantErrSubstring)
		}
		return true, nil
	})

	sch := scheduler.New(&stubTracker{issues: issues}, exec, resolver, scheduler.FixedBase("base"), 2)
	sch.PollInterval = time.Millisecond

	done := make(chan struct{})
	var results map[string]scheduler.Result
	var runErr error
	go func() {
		results, runErr = sch.Run(context.Background(), []string{"a", "c"})
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a to start")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after a dependency-check error (goroutine leak / hang)")
	}

	if runErr == nil || !strings.Contains(runErr.Error(), wantErrSubstring) {
		t.Fatalf("Run err = %v, want it to mention %q", runErr, wantErrSubstring)
	}
	if _, ok := results["a"]; !ok {
		t.Error(`results missing "a": the in-flight Worker must be waited for, not abandoned`)
	}
	if _, ok := results["c"]; ok {
		t.Error(`results contains "c": it should never have been dispatched`)
	}
	if got := exec.CallCount("c"); got != 0 {
		t.Errorf("CallCount(c) = %d, want 0", got)
	}
}

// fakeExternalResolver simulates the real seam ticket 27 wires in cmd/forge
// (a DependencyResolver backed by tracker.ExternalChecker, see
// cmd/forge's completionResolver): every dependsOnID not itself a
// requested/managed Issue is treated as an External Issue (CONTEXT.md) and
// gated on a scripted tracker.ExternalState rather than being reported an
// error, letting managed dependents unblock once their External
// prerequisite is EXTERNAL_SATISFIED and stay blocked (never dispatched,
// never added to the execution set) while it is EXTERNAL_PENDING or
// EXTERNAL_INVALID.
type fakeExternalResolver struct {
	managed map[string]bool

	mu     sync.Mutex
	states map[string]tracker.ExternalState
}

func (r *fakeExternalResolver) Satisfied(_ context.Context, _, dependsOnID string) (bool, error) {
	if r.managed[dependsOnID] {
		return true, nil // not exercised by these tests; managed deps use other resolvers above.
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.states[dependsOnID] == tracker.ExternalSatisfied, nil
}

func (r *fakeExternalResolver) setState(id string, state tracker.ExternalState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states[id] = state
}

// UnsatisfiedReason mirrors cmd/forge's completionResolver: EXTERNAL_INVALID
// keeps the "unsatisfiable" wording (it never resolves), EXTERNAL_PENDING
// gets a distinct, recheckable-sounding message (it may still resolve once
// a PR merges) — see scheduler.UnsatisfiedReasoner.
func (r *fakeExternalResolver) UnsatisfiedReason(_ context.Context, _, dependsOnID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.states[dependsOnID] {
	case tracker.ExternalInvalid:
		return fmt.Sprintf("external issue %s is EXTERNAL_INVALID (closed without a merged PR) and is permanently unsatisfiable", dependsOnID)
	case tracker.ExternalPending:
		return fmt.Sprintf("external issue %s is EXTERNAL_PENDING; re-run once its PR merges", dependsOnID)
	default:
		return ""
	}
}

var _ scheduler.DependencyResolver = (*fakeExternalResolver)(nil)
var _ scheduler.UnsatisfiedReasoner = (*fakeExternalResolver)(nil)

// TestRun_ExternalDependencySatisfied_ManagedDependentUnblocks is ticket
// 27's integration criterion: a managed Issue depending on an External
// Issue (outside the requested set) must stay undispatched while the
// external prerequisite is EXTERNAL_PENDING, then dispatch once a (fake)
// resolver reports it EXTERNAL_SATISFIED — and the external Issue itself
// must never be dispatched (it is never in the requested set at all).
//
// Issue "1" has no Dependencies and is kept in flight (gated, released only
// at the end) for the whole test: Run's poll loop only keeps rechecking
// Dependency satisfaction for still-blocked Issues while something is in
// flight (see Scheduler.Run's no-progress doc comment) — an External
// prerequisite can turn EXTERNAL_SATISFIED purely from the passage of time
// (a human merges a PR), with no local Worker driving that change, so
// without "1" here there would be nothing to keep Run polling at all.
func TestRun_ExternalDependencySatisfied_ManagedDependentUnblocks(t *testing.T) {
	issues := map[string]domain.Issue{
		"1": {ID: "1"},
		"2": {ID: "2", Dependencies: []domain.Dependency{{IssueID: "2", DependsOnID: "99"}}},
	}
	resolver := &fakeExternalResolver{managed: map[string]bool{"1": true}, states: map[string]tracker.ExternalState{"99": tracker.ExternalPending}}
	exec := newGatedExecutor()
	started := make(chan string, 2)
	exec.onDispatch = func(id string) { started <- id }

	sch := scheduler.New(&stubTracker{issues: issues}, exec, resolver, scheduler.FixedBase("base"), 2)
	sch.PollInterval = 2 * time.Millisecond

	done := make(chan map[string]scheduler.Result, 1)
	errCh := make(chan error, 1)
	go func() {
		results, err := sch.Run(context.Background(), []string{"1", "2"})
		errCh <- err
		done <- results
	}()

	// "1" dispatches immediately (no Dependencies) and stays in flight.
	select {
	case id := <-started:
		if id != "1" {
			t.Fatalf("dispatched %s first, want 1", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for issue 1 to dispatch")
	}

	// While pending, "2" must not dispatch.
	select {
	case id := <-started:
		t.Fatalf("issue %s dispatched while its external prerequisite is still EXTERNAL_PENDING", id)
	case <-time.After(20 * time.Millisecond):
	}

	resolver.setState("99", tracker.ExternalSatisfied)

	select {
	case id := <-started:
		if id != "2" {
			t.Fatalf("dispatched %s, want 2", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for issue 2 to dispatch after EXTERNAL_SATISFIED")
	}
	exec.releaseOne()
	exec.releaseOne()

	if err := <-errCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
	results := <-done
	if results["2"].State != domain.StateReviewing {
		t.Errorf("results[2].State = %s, want REVIEWING", results["2"].State)
	}
	if _, ok := results["99"]; ok {
		t.Error(`results contains "99": the External Issue must never be added to the execution set`)
	}
	if exec.CallCount("99") != 0 {
		t.Errorf("CallCount(99) = %d, want 0 (External Issues are never executed)", exec.CallCount("99"))
	}
}

// TestRun_ExternalDependencyInvalid_ManagedDependentStaysBlocked is ticket
// 27's other integration criterion: an External prerequisite that is
// closed without a merged PR (EXTERNAL_INVALID) never satisfies its
// dependent. Run must not hang — it reuses the no-progress (stall)
// detection from ticket 26 to surface an explicit "unsatisfiable
// dependency" Result instead of polling forever.
func TestRun_ExternalDependencyInvalid_ManagedDependentStaysBlocked(t *testing.T) {
	issues := map[string]domain.Issue{
		"2": {ID: "2", Dependencies: []domain.Dependency{{IssueID: "2", DependsOnID: "99"}}},
	}
	resolver := &fakeExternalResolver{managed: map[string]bool{}, states: map[string]tracker.ExternalState{"99": tracker.ExternalInvalid}}
	exec := newGatedExecutor()

	sch := scheduler.New(&stubTracker{issues: issues}, exec, resolver, scheduler.FixedBase("base"), 2)
	sch.PollInterval = 2 * time.Millisecond

	done := make(chan struct{})
	var results map[string]scheduler.Result
	var runErr error
	go func() {
		results, runErr = sch.Run(context.Background(), []string{"2"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return promptly for an EXTERNAL_INVALID prerequisite (hang)")
	}

	if runErr == nil || !strings.Contains(runErr.Error(), "unsatisfiable") {
		t.Fatalf("Run err = %v, want an unsatisfiable-dependency error", runErr)
	}
	if results["2"].Err == nil || !strings.Contains(results["2"].Err.Error(), "99") {
		t.Errorf("results[2].Err = %v, want it to name the invalid external prerequisite 99", results["2"].Err)
	}
	if results["2"].Err == nil || !strings.Contains(results["2"].Err.Error(), "EXTERNAL_INVALID") {
		t.Errorf("results[2].Err = %v, want it to identify EXTERNAL_INVALID as the reason (permanent, not recheckable)", results["2"].Err)
	}
	if exec.CallCount("2") != 0 {
		t.Errorf("CallCount(2) = %d, want 0 (must stay blocked, never dispatched)", exec.CallCount("2"))
	}
}

// TestRun_ExternalDependencyPending_LoneStallReportsRecheckable is the P3
// fix's regression test: when a lone dependent is blocked solely on an
// EXTERNAL_PENDING prerequisite (nothing else in flight to keep Run
// polling — see the EXTERNAL_SATISFIED test's doc comment), Run's stall
// message must not claim the dependency is "unsatisfiable" — it may well
// resolve once the PR merges — but should instead point the operator at
// re-running later.
func TestRun_ExternalDependencyPending_LoneStallReportsRecheckable(t *testing.T) {
	issues := map[string]domain.Issue{
		"2": {ID: "2", Dependencies: []domain.Dependency{{IssueID: "2", DependsOnID: "99"}}},
	}
	resolver := &fakeExternalResolver{managed: map[string]bool{}, states: map[string]tracker.ExternalState{"99": tracker.ExternalPending}}
	exec := newGatedExecutor()

	sch := scheduler.New(&stubTracker{issues: issues}, exec, resolver, scheduler.FixedBase("base"), 2)
	sch.PollInterval = 2 * time.Millisecond

	done := make(chan struct{})
	var results map[string]scheduler.Result
	var runErr error
	go func() {
		results, runErr = sch.Run(context.Background(), []string{"2"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return promptly for a lone EXTERNAL_PENDING prerequisite (hang)")
	}

	if runErr == nil {
		t.Fatal("Run: want a non-nil error (nothing was ever in flight to keep polling), got nil")
	}
	if strings.Contains(runErr.Error(), "unsatisfiable") {
		t.Errorf("Run err = %v, must not say 'unsatisfiable' for a transient EXTERNAL_PENDING block", runErr)
	}
	if results["2"].Err == nil || !strings.Contains(results["2"].Err.Error(), "EXTERNAL_PENDING") {
		t.Errorf("results[2].Err = %v, want it to identify EXTERNAL_PENDING as the (recheckable) reason", results["2"].Err)
	}
	if exec.CallCount("2") != 0 {
		t.Errorf("CallCount(2) = %d, want 0", exec.CallCount("2"))
	}
}

func TestRun_CycleRejected_NoWorkDispatched(t *testing.T) {
	issues := map[string]domain.Issue{
		"a": {ID: "a", Dependencies: []domain.Dependency{{IssueID: "a", DependsOnID: "b"}}},
		"b": {ID: "b", Dependencies: []domain.Dependency{{IssueID: "b", DependsOnID: "a"}}},
	}
	exec := newGatedExecutor()
	sch := scheduler.New(&stubTracker{issues: issues}, exec, alwaysSatisfied, scheduler.FixedBase("base"), 2)

	if _, err := sch.Run(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("Run: want error for a dependency cycle, got nil")
	}
	if exec.CallCount("a") != 0 || exec.CallCount("b") != 0 {
		t.Errorf("Executor was invoked despite a cycle: calls a=%d b=%d", exec.CallCount("a"), exec.CallCount("b"))
	}
}

func TestRun_NoIssues_ReturnsEmptyResults(t *testing.T) {
	exec := newGatedExecutor()
	sch := scheduler.New(&stubTracker{issues: map[string]domain.Issue{}}, exec, alwaysSatisfied, scheduler.FixedBase("base"), 2)
	results, err := sch.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results = %+v, want empty", results)
	}
}

func TestNew_DefaultsMaxParallelAndPollInterval(t *testing.T) {
	sch := scheduler.New(&stubTracker{}, newGatedExecutor(), alwaysSatisfied, scheduler.FixedBase("base"), 0)
	if sch.MaxParallel != 1 {
		t.Errorf("MaxParallel = %d, want 1 (floored)", sch.MaxParallel)
	}
	if sch.PollInterval <= 0 {
		t.Errorf("PollInterval = %v, want a positive default", sch.PollInterval)
	}
}

// --- Integration test against the real engine.Engine (ticket 26's
// suggested fixtures: FakeAgent, gittest temp repo, temp SQLite) ---

func openTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.Open(t.TempDir() + "/forge.db")
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

// TestRun_RealEngine_DependentIssueStartsFromMergedBase drives the actual
// engine.Engine (via Adapt), a real SQLite store, and a real Workspace
// manager against a temp git repo: "21" depends on "20"; once "20"
// finishes, a second commit lands on the repository's default branch
// (simulating its PR having merged), and "21" must be dispatched with that
// new commit as its base revision, not the Execution's original base.
func TestRun_RealEngine_DependentIssueStartsFromMergedBase(t *testing.T) {
	repoRoot, base := gittest.NewTempRepo(t)
	store := openTestStore(t)
	wsMgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	fake := agent.NewFakeAgent()
	fake.ProgramResult("20", agent.AgentResult{Status: agent.StatusImplemented})
	fake.ProgramResult("21", agent.AgentResult{Status: agent.StatusImplemented})

	eng := engine.New(store, &stubTracker{issues: map[string]domain.Issue{
		"20": {ID: "20"},
		"21": {ID: "21", Dependencies: []domain.Dependency{{IssueID: "21", DependsOnID: "20"}}},
	}}, wsMgr, fake, config.Default(), repoRoot)

	var mu sync.Mutex
	satisfied := map[string]bool{}
	mergedBase := ""

	resolver := scheduler.DependencyResolverFunc(func(_ context.Context, _, dependsOnID string) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		return satisfied[dependsOnID], nil
	})
	baseResolver := scheduler.BaseResolverFunc(func(_ context.Context, issueID string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if issueID == "21" && mergedBase != "" {
			return mergedBase, nil
		}
		return base, nil
	})

	sch := scheduler.New(&stubTracker{issues: map[string]domain.Issue{
		"20": {ID: "20"},
		"21": {ID: "21", Dependencies: []domain.Dependency{{IssueID: "21", DependsOnID: "20"}}},
	}}, scheduler.Adapt(eng), resolver, baseResolver, 2)
	sch.PollInterval = 2 * time.Millisecond
	sch.OnComplete = func(issueID string, _ domain.IssueState, err error) {
		if issueID != "20" || err != nil {
			return
		}
		// Simulate "20"'s PR merging: a new commit lands on the applicable
		// base branch after it finishes.
		gittest.RunGit(t, repoRoot, "commit", "--allow-empty", "-q", "-m", "merge 20")
		sha := strings.TrimSpace(gittest.RunGit(t, repoRoot, "rev-parse", "HEAD"))
		mu.Lock()
		satisfied["20"] = true
		mergedBase = sha
		mu.Unlock()
	}

	results, err := sch.Run(context.Background(), []string{"20", "21"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range []string{"20", "21"} {
		if results[id].State != domain.StateReviewing {
			t.Errorf("results[%s].State = %s, want REVIEWING", id, results[id].State)
		}
		if results[id].ExecutionID == "" {
			t.Errorf("results[%s].ExecutionID is empty, want the Execution ID Engine.Execute minted", id)
		}
	}

	invocations := fake.Invocations()
	var base20, base21 string
	for _, inv := range invocations {
		switch inv.Issue.ID {
		case "20":
			base20 = inv.Repository.BaseRevision
		case "21":
			base21 = inv.Repository.BaseRevision
		}
	}
	if base20 != base {
		t.Errorf("issue 20 BaseRevision = %s, want the Execution's starting base %s", base20, base)
	}
	if base21 == "" || base21 == base {
		t.Errorf("issue 21 BaseRevision = %s, want the post-merge commit, not the original base %s", base21, base)
	}
	mu.Lock()
	wantBase21 := mergedBase
	mu.Unlock()
	if base21 != wantBase21 {
		t.Errorf("issue 21 BaseRevision = %s, want %s", base21, wantBase21)
	}
}

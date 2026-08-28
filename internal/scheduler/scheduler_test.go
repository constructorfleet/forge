package scheduler_test

import (
	"context"
	"errors"
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

func (e *gatedExecutor) Execute(ctx context.Context, issueID, _ string) (domain.IssueState, error) {
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
	return domain.StateReviewing, nil
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

func (e *recordingExecutor) Execute(_ context.Context, issueID, base string) (domain.IssueState, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.order = append(e.order, issueID)
	if e.bases == nil {
		e.bases = map[string]string{}
	}
	e.bases[issueID] = base
	return domain.StateReviewing, nil
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

// TestRun_CycleRejected_NoWorkDispatched is the ticket's "reject cycles
// before any work" requirement: a cycle must be detected before Executor is
// ever invoked.
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

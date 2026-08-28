package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

// stubExternalChecker is a tracker.ExternalChecker double: it returns a
// scripted state per issue ID and counts how many times each was queried,
// so tests can assert on completionResolver's caching behavior (ticket 27:
// terminal states cache, EXTERNAL_PENDING never does, so a later poll or
// invocation re-evaluates against current refs).
type stubExternalChecker struct {
	mu     sync.Mutex
	states map[string]tracker.ExternalState
	errs   map[string]error
	calls  map[string]int
}

func newStubExternalChecker() *stubExternalChecker {
	return &stubExternalChecker{
		states: map[string]tracker.ExternalState{},
		errs:   map[string]error{},
		calls:  map[string]int{},
	}
}

func (s *stubExternalChecker) CheckExternal(_ context.Context, issueID, _ string) (tracker.ExternalState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[issueID]++
	if err, ok := s.errs[issueID]; ok {
		return "", err
	}
	return s.states[issueID], nil
}

func (s *stubExternalChecker) callCount(issueID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[issueID]
}

var _ tracker.ExternalChecker = (*stubExternalChecker)(nil)

func TestCompletionResolver_ManagedDependency_SatisfiedAtOrBeyondReviewing(t *testing.T) {
	r := newCompletionResolver([]string{"1", "2"}, newStubExternalChecker(), "origin/main")

	ok, err := r.Satisfied(context.Background(), "2", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected unsatisfied before prerequisite completes")
	}

	r.onComplete("1", domain.StateReviewing, nil)

	ok, err = r.Satisfied(context.Background(), "2", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected satisfied once prerequisite reaches REVIEWING")
	}
}

func TestCompletionResolver_ManagedDependency_FailedNeverSatisfies(t *testing.T) {
	r := newCompletionResolver([]string{"1", "2"}, newStubExternalChecker(), "origin/main")
	r.onComplete("1", domain.StateFailed, nil)

	ok, err := r.Satisfied(context.Background(), "2", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected FAILED to never satisfy a Dependency")
	}
}

func TestCompletionResolver_ExternalDependency_SatisfiedUnblocksManagedDependent(t *testing.T) {
	checker := newStubExternalChecker()
	checker.states["99"] = tracker.ExternalSatisfied
	r := newCompletionResolver([]string{"2"}, checker, "origin/main")

	ok, err := r.Satisfied(context.Background(), "2", "99")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected external issue 99 to satisfy the dependency once EXTERNAL_SATISFIED")
	}
}

func TestCompletionResolver_ExternalDependency_PendingStaysUnsatisfiedAndNeverAddedToSet(t *testing.T) {
	checker := newStubExternalChecker()
	checker.states["99"] = tracker.ExternalPending
	r := newCompletionResolver([]string{"2"}, checker, "origin/main")

	ok, err := r.Satisfied(context.Background(), "2", "99")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected EXTERNAL_PENDING to be unsatisfied")
	}
	if r.requested["99"] {
		t.Fatal("external issue must never be added to the requested execution set")
	}
}

func TestCompletionResolver_ExternalDependency_InvalidStaysUnsatisfiedWithoutError(t *testing.T) {
	// Closed-without-merge (EXTERNAL_INVALID) must not error: the
	// scheduler's own no-progress detection is what surfaces a
	// permanently-unsatisfiable dependency, not a special-cased error
	// here (see scheduler.Run's stall handling, reused rather than
	// duplicated).
	checker := newStubExternalChecker()
	checker.states["99"] = tracker.ExternalInvalid
	r := newCompletionResolver([]string{"2"}, checker, "origin/main")

	ok, err := r.Satisfied(context.Background(), "2", "99")
	if err != nil {
		t.Fatalf("expected no error for EXTERNAL_INVALID, got %v", err)
	}
	if ok {
		t.Fatal("expected EXTERNAL_INVALID to be unsatisfied")
	}
}

func TestCompletionResolver_ExternalDependency_CachesTerminalStatesOnly(t *testing.T) {
	checker := newStubExternalChecker()
	checker.states["99"] = tracker.ExternalPending
	r := newCompletionResolver([]string{"2"}, checker, "origin/main")

	// Pending: must re-check every call (never cached) so a later poll —
	// or a subsequent invocation, which always starts a fresh resolver —
	// observes newly-landed merges (ticket 27: "forge resume re-evaluates
	// external dependency state against current remote refs").
	for i := 0; i < 3; i++ {
		if _, err := r.Satisfied(context.Background(), "2", "99"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if got := checker.callCount("99"); got != 3 {
		t.Fatalf("callCount = %d, want 3 (EXTERNAL_PENDING must never be cached)", got)
	}

	checker.mu.Lock()
	checker.states["99"] = tracker.ExternalSatisfied
	checker.mu.Unlock()

	if ok, err := r.Satisfied(context.Background(), "2", "99"); err != nil || !ok {
		t.Fatalf("Satisfied = %v, %v; want true, nil once EXTERNAL_SATISFIED", ok, err)
	}
	// Satisfied: now cached, so a further call must not re-invoke checker.
	if ok, err := r.Satisfied(context.Background(), "2", "99"); err != nil || !ok {
		t.Fatalf("Satisfied = %v, %v; want true, nil (cached)", ok, err)
	}
	if got := checker.callCount("99"); got != 4 {
		t.Fatalf("callCount = %d, want 4 (one more check to observe SATISFIED, then cached)", got)
	}
}

func TestCompletionResolver_ExternalDependency_CheckerErrorPropagates(t *testing.T) {
	checker := newStubExternalChecker()
	checker.errs["99"] = errors.New("github: boom")
	r := newCompletionResolver([]string{"2"}, checker, "origin/main")

	_, err := r.Satisfied(context.Background(), "2", "99")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Satisfied err = %v, want it to wrap the checker's error", err)
	}
}

func TestCompletionResolver_NoCheckerConfigured_ExternalDependencyErrors(t *testing.T) {
	r := newCompletionResolver([]string{"2"}, nil, "origin/main")

	if _, err := r.Satisfied(context.Background(), "2", "99"); err == nil {
		t.Fatal("expected an error when no external checker is configured, got nil")
	}
}

// --- gitReachabilityChecker (the production GitReachabilityChecker) ---

func TestGitReachabilityChecker_IsAncestor(t *testing.T) {
	root, base := newTempRepo(t)
	checker := gitReachabilityChecker{repoRoot: root}

	ok, err := checker.IsAncestor(context.Background(), base, "HEAD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected the initial commit to be an ancestor of HEAD")
	}
}

func TestGitReachabilityChecker_NotAncestor(t *testing.T) {
	root, _ := newTempRepo(t)
	// A commit on an orphan branch shares no history with main.
	runGit(t, root, "checkout", "--orphan", "other")
	runGit(t, root, "commit", "--allow-empty", "-q", "-m", "unrelated")
	other := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	runGit(t, root, "checkout", "main")

	checker := gitReachabilityChecker{repoRoot: root}
	ok, err := checker.IsAncestor(context.Background(), other, "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected an unrelated commit not to be reported as an ancestor")
	}
}

// TestGitReachabilityChecker_UnknownCommitIsNotReachableRatherThanError is
// the P2 fix's regression test: a merge commit that hasn't been fetched
// into the local checkout yet (e.g. an external PR merged into a branch
// nothing has fetched) must be reported as "not yet reachable" — false,
// nil — not a hard error. `git merge-base --is-ancestor` alone can't tell
// "unknown commit" apart from "unknown base branch" (both exit 128), so
// IsAncestor must check the commit's local presence itself before running
// merge-base at all.
func TestGitReachabilityChecker_UnknownCommitIsNotReachableRatherThanError(t *testing.T) {
	root, _ := newTempRepo(t)
	checker := gitReachabilityChecker{repoRoot: root}

	ok, err := checker.IsAncestor(context.Background(), "0000000000000000000000000000000000000f", "main")
	if err != nil {
		t.Fatalf("unexpected error for a not-yet-fetched commit: %v", err)
	}
	if ok {
		t.Fatal("expected an unknown commit to be reported not-reachable, not ancestor")
	}
}

// TestGitReachabilityChecker_UnknownBaseBranchErrors confirms the other
// half of the P2 fix: once the commit itself is known locally, a genuinely
// bad/unknown base branch is still a loud error rather than being silently
// swallowed as "not reachable".
func TestGitReachabilityChecker_UnknownBaseBranchErrors(t *testing.T) {
	root, base := newTempRepo(t)
	checker := gitReachabilityChecker{repoRoot: root}

	if _, err := checker.IsAncestor(context.Background(), base, "does-not-exist"); err == nil {
		t.Fatal("expected an error for an unresolvable base branch, got nil")
	}
}

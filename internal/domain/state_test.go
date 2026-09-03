package domain_test

import (
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
)

// legalTransitions enumerates every transition the state machine must accept.
// Mirrors CONTEXT.md / IDEATION.md §16 "State transitions".
func legalTransitions() []struct {
	from domain.IssueState
	to   domain.IssueState
} {
	return []struct {
		from domain.IssueState
		to   domain.IssueState
	}{
		{domain.StatePending, domain.StateBlockedDependency},
		{domain.StatePending, domain.StateReady},
		{domain.StateBlockedDependency, domain.StateReady},
		{domain.StateReady, domain.StateClaimed},
		{domain.StateClaimed, domain.StatePreparing},
		{domain.StatePreparing, domain.StateImplementing},
		{domain.StateImplementing, domain.StateNeedsInfo},
		{domain.StateImplementing, domain.StateValidating},
		{domain.StateImplementing, domain.StateFailed},
		{domain.StateValidating, domain.StateImplementing},
		{domain.StateValidating, domain.StateReviewing},
		{domain.StateValidating, domain.StateFailed},
		{domain.StateReviewing, domain.StateImplementing},
		{domain.StateReviewing, domain.StateCommitting},
		{domain.StateReviewing, domain.StateFailed},
		{domain.StateCommitting, domain.StatePRCreating},
		{domain.StateCommitting, domain.StateNeedsInfo},
		{domain.StateCommitting, domain.StateFailed},
		{domain.StatePRCreating, domain.StateCIPending},
		{domain.StateCIPending, domain.StateCIFailed},
		{domain.StateCIPending, domain.StateDone},
		{domain.StateCIPending, domain.StateNeedsInfo},
		{domain.StateCIFailed, domain.StateImplementing},
		{domain.StateCIFailed, domain.StateFailed},
		{domain.StateNeedsInfo, domain.StateReady},
		{domain.StateImplementing, domain.StateNeedsReplan},
		{domain.StateCommitting, domain.StateNeedsReplan},
		{domain.StateNeedsReplan, domain.StateReady},
		{domain.StateNeedsReplan, domain.StateCancelled},
		{domain.StateImplementing, domain.StateProviderLimit},
		{domain.StateReviewing, domain.StateProviderLimit},
		{domain.StateProviderLimit, domain.StateReady},
		{domain.StateProviderLimit, domain.StateFailed},
		{domain.StateProviderLimit, domain.StateCancelled},
		{domain.StateFailed, domain.StateReady},
		// Manual cancellation is reachable from every non-terminal state.
		{domain.StatePending, domain.StateCancelled},
		{domain.StateBlockedDependency, domain.StateCancelled},
		{domain.StateReady, domain.StateCancelled},
		{domain.StateClaimed, domain.StateCancelled},
		{domain.StatePreparing, domain.StateCancelled},
		{domain.StateImplementing, domain.StateCancelled},
		{domain.StateValidating, domain.StateCancelled},
		{domain.StateReviewing, domain.StateCancelled},
		{domain.StateCommitting, domain.StateCancelled},
		{domain.StatePRCreating, domain.StateCancelled},
		{domain.StateCIPending, domain.StateCancelled},
		{domain.StateCIFailed, domain.StateCancelled},
		{domain.StateNeedsInfo, domain.StateCancelled},
	}
}

// allStates is every IssueState the state machine defines, in lifecycle
// order. One list rather than a copy per test, so adding a state (e.g.
// NEEDS_REPLAN, ticket 22) is a single edit that every exhaustive test
// picks up.
func allStates() []domain.IssueState {
	return []domain.IssueState{
		domain.StatePending,
		domain.StateBlockedDependency,
		domain.StateReady,
		domain.StateClaimed,
		domain.StatePreparing,
		domain.StateImplementing,
		domain.StateValidating,
		domain.StateReviewing,
		domain.StateCommitting,
		domain.StatePRCreating,
		domain.StateCIPending,
		domain.StateCIFailed,
		domain.StateNeedsInfo,
		domain.StateNeedsReplan,
		domain.StateProviderLimit,
		domain.StateFailed,
		domain.StateDone,
		domain.StateCancelled,
	}
}

func TestAllEighteenStatesAreDefined(t *testing.T) {
	want := allStates()
	if len(want) != 18 {
		t.Fatalf("test setup error: expected 18 states, got %d", len(want))
	}
	seen := make(map[domain.IssueState]bool)
	for _, s := range want {
		if s == "" {
			t.Fatalf("state constant has zero value")
		}
		if seen[s] {
			t.Fatalf("duplicate state value: %v", s)
		}
		seen[s] = true
	}
}

func TestLegalTransitionsAreAccepted(t *testing.T) {
	for _, tc := range legalTransitions() {
		t.Run(string(tc.from)+"->"+string(tc.to), func(t *testing.T) {
			if err := domain.ValidateTransition(tc.from, tc.to); err != nil {
				t.Fatalf("expected %s -> %s to be legal, got error: %v", tc.from, tc.to, err)
			}
		})
	}
}

func TestIllegalTransitionsAreRejected(t *testing.T) {
	cases := []struct {
		from domain.IssueState
		to   domain.IssueState
	}{
		// Cannot skip stages.
		{domain.StatePending, domain.StateClaimed},
		{domain.StateReady, domain.StateImplementing},
		{domain.StateClaimed, domain.StateDone},
		// Cannot move backward past the repair loop.
		{domain.StateDone, domain.StatePending},
		{domain.StateCommitting, domain.StateImplementing},
		// Terminal states have no outgoing transitions except manual retry
		// from FAILED.
		{domain.StateDone, domain.StateCancelled},
		{domain.StateCancelled, domain.StateReady},
		// Self-transitions are not legal moves.
		{domain.StateImplementing, domain.StateImplementing},
		// Unrelated lateral jumps.
		{domain.StateNeedsInfo, domain.StateDone},
		{domain.StateCIFailed, domain.StateDone},
	}

	for _, tc := range cases {
		t.Run(string(tc.from)+"->"+string(tc.to), func(t *testing.T) {
			err := domain.ValidateTransition(tc.from, tc.to)
			if err == nil {
				t.Fatalf("expected %s -> %s to be rejected", tc.from, tc.to)
			}
			var transErr *domain.InvalidTransitionError
			if !errors.As(err, &transErr) {
				t.Fatalf("expected *domain.InvalidTransitionError, got %T: %v", err, err)
			}
			if transErr.From != tc.from || transErr.To != tc.to {
				t.Fatalf("error does not describe the attempted transition: %+v", transErr)
			}
		})
	}
}

func TestTerminalStatesHaveNoOutgoingTransitions(t *testing.T) {
	terminal := []domain.IssueState{domain.StateDone, domain.StateCancelled}
	all := allStates()
	for _, from := range terminal {
		for _, to := range all {
			if err := domain.ValidateTransition(from, to); err == nil {
				t.Fatalf("terminal state %s should reject transition to %s", from, to)
			}
		}
	}
}

func TestFailedOnlyAllowsManualRetry(t *testing.T) {
	allowed := map[domain.IssueState]bool{domain.StateReady: true}
	all := allStates()
	for _, to := range all {
		err := domain.ValidateTransition(domain.StateFailed, to)
		if allowed[to] {
			if err != nil {
				t.Fatalf("FAILED -> %s should be legal, got %v", to, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("FAILED -> %s should be rejected", to)
		}
	}
}

// TestNeedsReplanOnlyExitsAreReadyAndCancelled pins acceptance item 1: the
// NEEDS_REPLAN state a REPLAN_REQUIRED escalation parks an Issue in has
// exactly two exits — back to READY once a fresh plan is approved, or
// CANCELLED when the Issue is superseded by that plan.
func TestNeedsReplanOnlyExitsAreReadyAndCancelled(t *testing.T) {
	allowed := map[domain.IssueState]bool{
		domain.StateReady:     true,
		domain.StateCancelled: true,
	}
	for _, to := range allStates() {
		err := domain.ValidateTransition(domain.StateNeedsReplan, to)
		if allowed[to] {
			if err != nil {
				t.Fatalf("NEEDS_REPLAN -> %s should be legal, got %v", to, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("NEEDS_REPLAN -> %s should be rejected", to)
		}
	}
}

// TestNeedsReplanIsReachableOnlyFromImplementingAndCommitting pins the two
// entry points: the Agent's own structural escalation (IMPLEMENTING) and the
// integration gate an in-flight Worker hits after committing (COMMITTING).
func TestNeedsReplanIsReachableOnlyFromImplementingAndCommitting(t *testing.T) {
	allowed := map[domain.IssueState]bool{
		domain.StateImplementing: true,
		domain.StateCommitting:   true,
	}
	for _, from := range allStates() {
		err := domain.ValidateTransition(from, domain.StateNeedsReplan)
		if allowed[from] {
			if err != nil {
				t.Fatalf("%s -> NEEDS_REPLAN should be legal, got %v", from, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s -> NEEDS_REPLAN should be rejected", from)
		}
	}
}

// TestProviderLimitOnlyExitsAreReadyFailedAndCancelled pins the exits of the
// PROVIDER_LIMIT state. The controller returns the Issue to READY when the
// backoff time passes and the provider-limit retry budget still has room. The
// Issue moves to FAILED when that budget is exhausted. CANCELLED is the
// generic manual exit every non-terminal state has.
func TestProviderLimitOnlyExitsAreReadyFailedAndCancelled(t *testing.T) {
	allowed := map[domain.IssueState]bool{
		domain.StateReady:     true,
		domain.StateFailed:    true,
		domain.StateCancelled: true,
	}
	for _, to := range allStates() {
		err := domain.ValidateTransition(domain.StateProviderLimit, to)
		if allowed[to] {
			if err != nil {
				t.Fatalf("PROVIDER_LIMIT -> %s should be legal, got %v", to, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("PROVIDER_LIMIT -> %s should be rejected", to)
		}
	}
}

// TestProviderLimitIsReachableOnlyFromAgentStages pins the entry points. The
// Agent can report a provider limit during IMPLEMENTING. A review axis can
// report one during REVIEWING.
func TestProviderLimitIsReachableOnlyFromAgentStages(t *testing.T) {
	allowed := map[domain.IssueState]bool{
		domain.StateImplementing: true,
		domain.StateReviewing:    true,
	}
	for _, from := range allStates() {
		err := domain.ValidateTransition(from, domain.StateProviderLimit)
		if allowed[from] {
			if err != nil {
				t.Fatalf("%s -> PROVIDER_LIMIT should be legal, got %v", from, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s -> PROVIDER_LIMIT should be rejected", from)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	terminal := map[domain.IssueState]bool{
		domain.StateDone:      true,
		domain.StateCancelled: true,
		domain.StateFailed:    true,
	}
	all := allStates()
	for _, s := range all {
		want := terminal[s]
		if got := s.IsTerminal(); got != want {
			t.Fatalf("%s.IsTerminal() = %v, want %v", s, got, want)
		}
	}
}

func TestInvalidTransitionErrorMessage(t *testing.T) {
	err := &domain.InvalidTransitionError{From: domain.StatePending, To: domain.StateDone}
	want := "invalid issue state transition: PENDING -> DONE is not a legal transition"
	if err.Error() != want {
		t.Fatalf("unexpected error message: got %q, want %q", err.Error(), want)
	}
}

func TestIssueApplyTransitionMutatesState(t *testing.T) {
	issue := domain.Issue{State: domain.StatePending}
	if err := issue.ApplyTransition(domain.StateReady); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.State != domain.StateReady {
		t.Fatalf("expected state READY, got %s", issue.State)
	}

	if err := issue.ApplyTransition(domain.StateDone); err == nil {
		t.Fatalf("expected illegal transition to be rejected and state left unchanged")
	}
	if issue.State != domain.StateReady {
		t.Fatalf("issue state must not change on rejected transition, got %s", issue.State)
	}
}

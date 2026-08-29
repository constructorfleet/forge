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
		{domain.StateCommitting, domain.StateFailed},
		{domain.StatePRCreating, domain.StateCIPending},
		{domain.StateCIPending, domain.StateCIFailed},
		{domain.StateCIPending, domain.StateDone},
		{domain.StateCIFailed, domain.StateImplementing},
		{domain.StateCIFailed, domain.StateFailed},
		{domain.StateNeedsInfo, domain.StateReady},
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

func TestAllSixteenStatesAreDefined(t *testing.T) {
	want := []domain.IssueState{
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
		domain.StateFailed,
		domain.StateDone,
		domain.StateCancelled,
	}
	if len(want) != 16 {
		t.Fatalf("test setup error: expected 16 states, got %d", len(want))
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
	all := []domain.IssueState{
		domain.StatePending, domain.StateBlockedDependency, domain.StateReady,
		domain.StateClaimed, domain.StatePreparing, domain.StateImplementing,
		domain.StateValidating, domain.StateReviewing, domain.StateCommitting,
		domain.StatePRCreating, domain.StateCIPending, domain.StateCIFailed,
		domain.StateNeedsInfo, domain.StateFailed, domain.StateDone, domain.StateCancelled,
	}
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
	all := []domain.IssueState{
		domain.StatePending, domain.StateBlockedDependency, domain.StateReady,
		domain.StateClaimed, domain.StatePreparing, domain.StateImplementing,
		domain.StateValidating, domain.StateReviewing, domain.StateCommitting,
		domain.StatePRCreating, domain.StateCIPending, domain.StateCIFailed,
		domain.StateNeedsInfo, domain.StateFailed, domain.StateDone, domain.StateCancelled,
	}
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

func TestIsTerminal(t *testing.T) {
	terminal := map[domain.IssueState]bool{
		domain.StateDone:      true,
		domain.StateCancelled: true,
		domain.StateFailed:    true,
	}
	all := []domain.IssueState{
		domain.StatePending, domain.StateBlockedDependency, domain.StateReady,
		domain.StateClaimed, domain.StatePreparing, domain.StateImplementing,
		domain.StateValidating, domain.StateReviewing, domain.StateCommitting,
		domain.StatePRCreating, domain.StateCIPending, domain.StateCIFailed,
		domain.StateNeedsInfo, domain.StateFailed, domain.StateDone, domain.StateCancelled,
	}
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

package domain

import "fmt"

// IssueState is one of the 16 states an Issue moves through over its
// lifetime. See CONTEXT.md "Issue states" and IDEATION.md §15-16.
type IssueState string

const (
	StatePending           IssueState = "PENDING"
	StateBlockedDependency IssueState = "BLOCKED_DEPENDENCY"
	StateReady             IssueState = "READY"
	StateClaimed           IssueState = "CLAIMED"
	StatePreparing         IssueState = "PREPARING"
	StateImplementing      IssueState = "IMPLEMENTING"
	StateValidating        IssueState = "VALIDATING"
	StateReviewing         IssueState = "REVIEWING"
	StateCommitting        IssueState = "COMMITTING"
	StatePRCreating        IssueState = "PR_CREATING"
	StateCIPending         IssueState = "CI_PENDING"
	StateCIFailed          IssueState = "CI_FAILED"
	StateNeedsInfo         IssueState = "NEEDS_INFO"
	StateFailed            IssueState = "FAILED"
	StateDone              IssueState = "DONE"
	StateCancelled         IssueState = "CANCELLED"
)

// terminalStates are the states with no outgoing transitions: DONE and
// CANCELLED are successful/aborted end states, and FAILED is a terminal
// end state reached only via retry-budget exhaustion (see CONTEXT.md
// "Retry Budget").
var terminalStates = map[IssueState]bool{
	StateDone:      true,
	StateCancelled: true,
	StateFailed:    true,
}

// IsTerminal reports whether an Issue in this state can make any further
// transitions.
func (s IssueState) IsTerminal() bool { return terminalStates[s] }

// InvalidTransitionError describes a rejected state transition attempt.
type InvalidTransitionError struct {
	From IssueState
	To   IssueState
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("invalid issue state transition: %s -> %s is not a legal transition", e.From, e.To)
}

// transitions is the forward-edge table, derived from IDEATION.md §16
// "State transitions" plus the retry-exhaustion paths to FAILED (CONTEXT.md
// "Retry Budget"; issue 09) and the needs-info resume flow (issue 07).
//
// Manual cancellation (-> CANCELLED) is legal from any non-terminal state
// and is handled once in ValidateTransition rather than repeated per row.
// Terminal states (see terminalStates) have no entry here and no outgoing
// transitions at all, including to CANCELLED. Manual retry from FAILED is
// handled once in ValidateTransition rather than encoded here as an
// ordinary workflow edge.
var transitions = map[IssueState][]IssueState{
	StatePending:           {StateBlockedDependency, StateReady},
	StateBlockedDependency: {StateReady},
	StateReady:             {StateClaimed},
	StateClaimed:           {StatePreparing},
	StatePreparing:         {StateImplementing},
	StateImplementing:      {StateNeedsInfo, StateValidating, StateFailed},
	StateValidating:        {StateImplementing, StateReviewing, StateFailed},
	StateReviewing:         {StateImplementing, StateCommitting, StateFailed},
	StateCommitting:        {StatePRCreating},
	StatePRCreating:        {StateCIPending},
	StateCIPending:         {StateCIFailed, StateDone},
	StateCIFailed:          {StateImplementing, StateFailed},
	StateNeedsInfo:         {StateReady},
}

// ValidateTransition reports whether moving an Issue from `from` to `to` is
// legal. It returns an *InvalidTransitionError describing the attempted
// transition when it is not. FAILED stays terminal for ordinary automatic
// orchestration, but a human-triggered retry may move it back to READY.
func ValidateTransition(from, to IssueState) error {
	if from == StateFailed && to == StateReady {
		return nil
	}
	if from.IsTerminal() {
		return &InvalidTransitionError{From: from, To: to}
	}
	if to == StateCancelled {
		return nil
	}
	for _, candidate := range transitions[from] {
		if candidate == to {
			return nil
		}
	}
	return &InvalidTransitionError{From: from, To: to}
}

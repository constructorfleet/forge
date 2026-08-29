package domain

import "fmt"

// IssueState is one of the 17 states an Issue moves through over its
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
	StateNeedsReplan       IssueState = "NEEDS_REPLAN"
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
// "Retry Budget"; issue 09), the needs-info resume flow (issue 07), and the
// empty-diff pre-PR guard's COMMITTING -> FAILED edge (engine.guardEmptyDiff;
// issue 09/26): an Agent that reports StatusImplemented without actually
// changing anything is caught right before PR_CREATING rather than opening
// an empty pull request.
//
// Manual cancellation (-> CANCELLED) is legal from any non-terminal state
// and is handled once in ValidateTransition rather than repeated per row.
// Terminal states (see terminalStates) have no entry here and no outgoing
// transitions at all, including to CANCELLED. Manual retry from FAILED is
// handled once in ValidateTransition rather than encoded here as an
// ordinary workflow edge.
//
// NEEDS_REPLAN (ticket 22, conservative replanning) is reachable from two
// places, and from those two only:
//
//   - IMPLEMENTING, when the Agent itself reports REPLAN_REQUIRED — the
//     structural escalation NEEDS_INFO's IMPLEMENTING edge is modelled on.
//   - COMMITTING, when the Feature was frozen while this Worker was still
//     in flight: the Worker is allowed to finish committing (its safe
//     suspension boundary) but is parked here instead of advancing to
//     PR_CREATING, which would integrate work against the invalidated plan.
//
// Its only workflow exit is back to READY (plus the generic CANCELLED edge
// every non-terminal state has, which is how an Issue absent from the newly
// approved plan is closed as superseded).
var transitions = map[IssueState][]IssueState{
	StatePending:           {StateBlockedDependency, StateReady},
	StateBlockedDependency: {StateReady},
	StateReady:             {StateClaimed},
	StateClaimed:           {StatePreparing},
	StatePreparing:         {StateImplementing},
	StateImplementing:      {StateNeedsInfo, StateNeedsReplan, StateValidating, StateFailed},
	StateValidating:        {StateImplementing, StateReviewing, StateFailed},
	StateReviewing:         {StateImplementing, StateCommitting, StateFailed},
	StateCommitting:        {StatePRCreating, StateNeedsReplan, StateFailed},
	StatePRCreating:        {StateCIPending},
	StateCIPending:         {StateCIFailed, StateDone},
	StateCIFailed:          {StateImplementing, StateFailed},
	StateNeedsInfo:         {StateReady},
	StateNeedsReplan:       {StateReady},
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

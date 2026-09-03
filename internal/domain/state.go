package domain

import "fmt"

// IssueState is one of the 18 states an Issue moves through over its
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
	StateProviderLimit     IssueState = "PROVIDER_LIMIT"
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

// StateGroup is the canonical coarse grouping of IssueState, shared by the
// TUI's Worker roster and the status display so both read a state alike.
// LOST is not an IssueState and has no group here.
type StateGroup string

const (
	// GroupPending: not yet started.
	GroupPending StateGroup = "pending"
	// GroupWorking: Forge is actively working the Issue.
	GroupWorking StateGroup = "working"
	// GroupWaiting: a pull request exists; Forge waits on CI or repairs it.
	GroupWaiting StateGroup = "waiting"
	// GroupBlocked: parked until human input or a provider backoff clears.
	GroupBlocked StateGroup = "blocked"
	// GroupFailed: terminal, reached via retry-budget exhaustion.
	GroupFailed StateGroup = "failed"
	// GroupDone: terminal, successful or aborted.
	GroupDone StateGroup = "done"
)

// Group returns the canonical coarse bucket for the state. Every IssueState
// maps to exactly one bucket.
func (s IssueState) Group() StateGroup {
	switch s {
	case StatePending, StateBlockedDependency, StateReady:
		return GroupPending
	case StateClaimed, StatePreparing, StateImplementing, StateValidating,
		StateReviewing, StateCommitting, StatePRCreating:
		return GroupWorking
	case StateCIPending, StateCIFailed:
		return GroupWaiting
	case StateNeedsInfo, StateNeedsReplan, StateProviderLimit:
		return GroupBlocked
	case StateFailed:
		return GroupFailed
	case StateDone, StateCancelled:
		return GroupDone
	default:
		// Zero value or an unknown state never group; keep it explicit so a
		// future state that omits a bucket is caught by the table test.
		return StateGroup("")
	}
}

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
// empty-diff pre-publication guard's COMMITTING -> NEEDS_INFO edge
// (engine.guardEmptyDiff): an Agent that reports StatusImplemented without
// actually changing anything is caught before Forge commits, pushes, or
// opens an empty pull request.
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
//
// PROVIDER_LIMIT parks an Issue whose Agent stopped because the model
// provider applied a rate or quota limit. It has two entry points:
//
//   - IMPLEMENTING, when the Agent reports agent.StatusProviderLimit — the
//     implementation Agent stopped before validation.
//   - REVIEWING, when one Review axis reports agent.StatusProviderLimit and
//     the Review cannot certify complete coverage.
//
// A provider limit is an external transient condition, not a defect in the
// Agent's work, so Forge waits rather than repairs. The state is not
// terminal. It has two workflow exits (plus the generic CANCELLED edge):
//
//   - READY, when the backoff time passes and the provider-limit retry
//     budget still has room. engine.ProviderLimitController takes this edge
//     automatically.
//   - FAILED, when the provider-limit retry budget is exhausted. The Issue
//     then rests in the same terminal state every other exhausted budget
//     reaches, and its state history shows the provider limit as the cause.
var transitions = map[IssueState][]IssueState{
	StatePending:           {StateBlockedDependency, StateReady},
	StateBlockedDependency: {StateReady},
	StateReady:             {StateClaimed},
	StateClaimed:           {StatePreparing},
	StatePreparing:         {StateImplementing},
	StateImplementing:      {StateNeedsInfo, StateNeedsReplan, StateProviderLimit, StateValidating, StateFailed},
	StateValidating:        {StateImplementing, StateReviewing, StateFailed},
	// Reviewing -> NeedsInfo (issue #161, review degradation): a Review that
	// cannot certify full axis coverage (review.VerdictInconclusive) or that
	// stays at CHANGES_REQUIRED once RetryBudget.Review is exhausted routes
	// here instead of to FAILED — the same human-escalation resting state
	// IMPLEMENTING and CIPending already use, since neither case is
	// something an automated repair attempt should improvise past. See
	// engine.escalateReviewToNeedsInfo.
	StateReviewing:  {StateImplementing, StateCommitting, StateNeedsInfo, StateProviderLimit, StateFailed},
	StateCommitting: {StatePRCreating, StateNeedsReplan, StateNeedsInfo, StateFailed},
	StatePRCreating: {StateCIPending},
	// CIPending -> NeedsInfo (issue 109, "PR supervision"): the CI
	// Supervisor's poll loop (internal/ci.Supervisor.Wait) also inspects
	// pull-request mergeability and review feedback alongside required
	// checks. A detected merge conflict, or review feedback ambiguous
	// enough that automated repair would be guessing at intent, is routed
	// to NEEDS_INFO — the same human-input resting state IMPLEMENTING
	// uses, and the same NEEDS_INFO -> READY resume flow (see
	// engine.Resume) — rather than to CI_FAILED, which is reserved for
	// failures the existing repair loop can act on unsupervised (a failed
	// check, or a single reviewer's actionable CHANGES_REQUESTED review).
	StateCIPending:     {StateCIFailed, StateDone, StateNeedsInfo},
	StateCIFailed:      {StateImplementing, StateFailed},
	StateNeedsInfo:     {StateReady},
	StateNeedsReplan:   {StateReady},
	StateProviderLimit: {StateReady, StateFailed},
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

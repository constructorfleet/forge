package domain

import "time"

// PlanningStatus is a Planning Execution's runtime status. Unlike
// IssueState, it is not a 16-state coding machine — it is a flat set of
// resting/terminal states describing whether the run is making progress,
// blocked on a human, blocked on an approval, or done. Stage and artifact
// freshness are deliberately NOT part of this type: they are derived from
// the Feature's Planning Artifacts on disk (internal/planning.Stale,
// Approved, Ready) every time they're needed, never persisted, so they can
// never drift from the files that are the actual source of truth.
type PlanningStatus string

const (
	PlanningStatusActive        PlanningStatus = "ACTIVE"
	PlanningStatusNeedsHuman    PlanningStatus = "NEEDS_HUMAN"
	PlanningStatusNeedsApproval PlanningStatus = "NEEDS_APPROVAL"
	PlanningStatusFailed        PlanningStatus = "FAILED"
	PlanningStatusComplete      PlanningStatus = "COMPLETE"
)

// Valid reports whether s is one of the recognized PlanningStatus values.
func (s PlanningStatus) Valid() bool {
	switch s {
	case PlanningStatusActive, PlanningStatusNeedsHuman, PlanningStatusNeedsApproval, PlanningStatusFailed, PlanningStatusComplete:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether s is a resting state a Planning Execution
// never leaves on its own (FAILED, COMPLETE) — the planning analogue of
// IssueState.IsTerminal.
func (s PlanningStatus) IsTerminal() bool {
	return s == PlanningStatusFailed || s == PlanningStatusComplete
}

// PlanningExecution is a user-requested `forge plan` run scoped to one
// Feature. It reuses the Execution/claim/PID-liveness persistence pattern
// Phase 1 established for coding Executions (see internal/engine,
// internal/storage) as a generic runtime container, but carries its own
// Status rather than participating in domain/state.go's coding machine.
type PlanningExecution struct {
	ID           string
	FeatureID    string
	BaseRevision string
	Status       PlanningStatus
	StartedAt    time.Time
}

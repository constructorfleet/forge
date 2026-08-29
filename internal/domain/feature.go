package domain

// Feature is a goal-only unit of planning work: a user-authored goal
// (CONTEXT.md's Planning Artifacts) that is developed into Decisions, a
// Spec, and a Ticket Plan before any Issue exists. Unlike Issue, a Feature
// carries no execution state machine, RetryBudget, or Dependencies — those
// are coding-execution concerns a goal-only Feature has not yet reached. A
// Feature's identity is its ID; its content lives entirely in the Planning
// Artifacts on disk, not in this struct.
type Feature struct {
	ID   string
	Name string
}

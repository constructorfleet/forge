package planning

// Stage identifies which step of the Planning Artifact pipeline (goal ->
// decisions -> spec -> ticket-plan) a Feature is currently working
// through.
type Stage string

const (
	// StageGoal means the Feature has no goal Artifact yet.
	StageGoal Stage = "goal"
	// StageDecisions means a goal exists but no decision Artifacts have
	// been recorded yet.
	StageDecisions Stage = "decisions"
	// StageSpec means decisions exist but no spec Artifact exists yet.
	StageSpec Stage = "spec"
	// StageTicketPlan means a spec exists but no ticket-plan Artifact
	// exists yet.
	StageTicketPlan Stage = "ticket-plan"
	// StageDone means every Planning Artifact in the pipeline exists.
	StageDone Stage = "done"
)

// DeriveStage returns the Planning stage a Feature is currently working
// through, computed purely from which Planning Artifacts exist for it —
// there is no stored stage, matching the package's "no stored freshness or
// approval bit" design (see the package doc comment and Stale/Approved).
// goal, spec, and ticketPlan are nil when that Artifact has not been
// created yet; decisions is every decision Artifact recorded so far
// (possibly empty).
func DeriveStage(goal *Artifact, decisions []*Artifact, spec *Artifact, ticketPlan *Artifact) Stage {
	switch {
	case goal == nil:
		return StageGoal
	case len(decisions) == 0:
		return StageDecisions
	case spec == nil:
		return StageSpec
	case ticketPlan == nil:
		return StageTicketPlan
	default:
		return StageDone
	}
}

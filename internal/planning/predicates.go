package planning

// Stale reports whether a's content has changed since its Revision field
// was last recorded: true when the recomputed content revision no longer
// matches a.Revision. This is the only source of truth for freshness —
// there is no separate stored flag. Render (which recomputes Revision
// before writing) always produces a non-stale artifact; Stale only fires
// for hand-edits that changed definitional content without updating the
// revision field.
func Stale(a *Artifact) bool {
	return ComputeRevision(a) != a.Revision
}

// Approved reports whether a's current content is the content that was
// approved: true when ApprovedRevision is set and matches the recomputed
// content revision. Editing definitional content after approval
// un-approves it automatically, since there is no separate approval bit
// to go stale.
func Approved(a *Artifact) bool {
	return a.ApprovedRevision != "" && a.ApprovedRevision == ComputeRevision(a)
}

// Reviewed reports whether a's current content is the content that was
// reviewed: true when State is "reviewed" and ReviewedRevision matches the
// recomputed content revision. Editing definitional content after review
// un-reviews it automatically, the same way editing content after approval
// un-approves it (Approved).
func Reviewed(a *Artifact) bool {
	return a.State == "reviewed" && a.ReviewedRevision != "" && a.ReviewedRevision == ComputeRevision(a)
}

// MarkReviewed stamps a as reviewed at its current content revision: it
// sets State to "reviewed" and ReviewedRevision to the recomputed content
// revision, together, so the two fields never go out of sync with each
// other the way four hand-written copies of this pair once could.
func MarkReviewed(a *Artifact) {
	a.State = "reviewed"
	a.ReviewedRevision = ComputeRevision(a)
}

// Legacy reports whether a has never been touched by review-tracking: both
// State and ReviewedRevision are empty. Every Specification or TicketPlan
// Artifact that a Planning Execution ever pauses on for approval passed a
// mandatory automated review first; State and ReviewedRevision only stay
// empty for artifacts written before that review outcome was recorded on
// the artifact itself. A Legacy artifact is treated as already reviewed,
// not permanently blocked from approval.
func Legacy(a *Artifact) bool {
	return a.State == "" && a.ReviewedRevision == ""
}

// Ready reports whether a decision Artifact is ready to be relied on by
// downstream artifacts: it must be a decision and currently approved.
// Ready is false for any other Kind.
func Ready(decision *Artifact) bool {
	return decision.Kind == KindDecision && Approved(decision)
}

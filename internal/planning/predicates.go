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

// Ready reports whether a decision Artifact is ready to be relied on by
// downstream artifacts: it must be a decision and currently approved.
// Ready is false for any other Kind.
func Ready(decision *Artifact) bool {
	return decision.Kind == KindDecision && Approved(decision)
}

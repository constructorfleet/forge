// Package planning parses and renders Planning Artifacts (goal.md,
// decisions/NNN-<slug>.md, spec.md, ticket-plan.md): markdown files that
// pair a strictly machine-owned `<!-- forge ... -->` metadata block with
// human-authored `##` sections. It also implements the content-revision and
// derived-predicate machinery (Stale, Approved, Ready) that later tickets
// build on. Everything here operates purely on durable file content -- there
// is no stored freshness or approval bit.
package planning

// Kind identifies the kind of Planning Artifact.
type Kind string

const (
	KindGoal       Kind = "goal"
	KindDecision   Kind = "decision"
	KindSpec       Kind = "spec"
	KindTicketPlan Kind = "ticket-plan"
)

// DerivedFromEntry records one artifact this Artifact was derived from, at
// the revision it was derived from. Entries are typed (Kind) and keyed
// (ID); canonicalization sorts them by ID so reordering the block does not
// change the revision.
type DerivedFromEntry struct {
	Kind     Kind
	ID       string
	Revision string
}

// Section is one human `##` section: the heading text (without the `## `
// prefix) and its raw body. A Section with an empty Heading holds content
// that appeared before the first `##` heading, if any.
type Section struct {
	Heading string
	Body    string
}

// Artifact is the parsed form of a Planning Artifact file.
//
// Revision, State, ApprovedRevision, ApprovedBy, and ApprovedAt are
// workflow fields: they record process state but do not participate in the
// content revision. Kind, DerivedFrom, and Sections are definitional: they
// are exactly what ComputeRevision hashes.
type Artifact struct {
	Kind Kind

	// Revision is the content-revision recorded in the file's metadata
	// block as of the last save. Compare against ComputeRevision to detect
	// hand-edits that were not accompanied by a recomputed revision.
	Revision string

	// State is a free-form workflow state string (e.g. "draft",
	// "proposed"). Planning does not interpret it.
	State string

	// ApprovedRevision is the content revision that was approved, if any.
	ApprovedRevision string
	ApprovedBy       string
	ApprovedAt       string

	DerivedFrom []DerivedFromEntry
	Sections    []Section
}

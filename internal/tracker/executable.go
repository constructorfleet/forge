package tracker

import "fmt"

// NotExecutableError is returned by ValidateExecutable when an Issue's
// `## Forge Provenance` block reports it is not yet materialization-ready
// — either because it is still mid-materialization
// (ProvenanceMaterializing) or because its provenance block is malformed,
// which is treated identically (fail closed) rather than guessed at.
type NotExecutableError struct {
	IssueID string
	Reason  string
}

func (e *NotExecutableError) Error() string {
	return fmt.Sprintf("tracker: issue %s is not executable: %s", e.IssueID, e.Reason)
}

// ValidateExecutable rejects an Issue whose `## Forge Provenance` block
// (see ParseForgeProvenance) marks it as not yet materialized — Phase 1's
// handoff gate (see the Materialization ticket: "Issues become executable
// only once the whole graph validates"; "Phase 1 ... rejects stale
// Forge-generated planning metadata"). An Issue with no Forge Provenance
// block at all is not materialization-gated (e.g. hand-created Issues
// predating the planning compiler) and is always executable.
func ValidateExecutable(issueID, body string) error {
	prov, err := ParseForgeProvenance(body)
	if err != nil {
		return &NotExecutableError{IssueID: issueID, Reason: fmt.Sprintf("malformed Forge Provenance block: %v", err)}
	}
	if prov == nil {
		// Not materialization-gated.
		return nil
	}
	if prov.Status != ProvenanceReady {
		return &NotExecutableError{IssueID: issueID, Reason: fmt.Sprintf("materialization status is %q, not %q", prov.Status, ProvenanceReady)}
	}
	return nil
}

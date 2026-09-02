package domain

import "time"

// ReviewOverride records one review finding Forge has determined does not
// converge across retries (issue #375): the reviewer raises the identical
// objection no matter how many repair attempts run. It is keyed by IssueID
// alone, not by Execution, so a later re-run of the same Issue — even
// within a new Execution — sees it and does not spend its review retry
// budget on the same non-defect again.
type ReviewOverride struct {
	IssueID   string
	Signature string
	Axis      string
	File      string
	Line      int
	Message   string
	Reason    string
	CreatedAt time.Time
}

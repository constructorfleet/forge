package gitlab

import (
	"github.com/Teagan42/forge/internal/tracker"
)

// pipelineCheckName is the one check name the GitLab CI capability reports.
// GitLab gates a merge request on one pipeline, not on many named checks, so
// the adapter presents that pipeline as a single neutral check. A project
// that requires the pipeline to pass therefore names this same string in its
// Merge Requirements (see GetMergeRequirements), and the generic
// check-against-requirement comparison in internal/ci works unchanged.
const pipelineCheckName = "pipeline"

// checksOf normalizes a merge request's head pipeline into the neutral check
// list. It reports one check, or none when the project ran no pipeline for
// the merge request yet. No pipeline is not a failure: a project can have no
// CI configured at all.
func checksOf(mr glMergeRequest) []tracker.Check {
	if mr.Pipeline == nil {
		return []tracker.Check{}
	}
	return []tracker.Check{{
		Name:    pipelineCheckName,
		State:   normalizePipelineStatus(mr.Pipeline.Status),
		Details: mr.Pipeline.Status,
	}}
}

// normalizePipelineStatus maps a GitLab pipeline status onto the neutral
// check state. "success" is the only status that passes. "failed" and
// "canceled" are the only statuses that fail. Every other status, including
// one Forge does not know, is pending, so a pipeline that still runs is never
// misreported as done.
func normalizePipelineStatus(status string) tracker.CheckState {
	switch status {
	case "success":
		return tracker.CheckSuccess
	case "failed", "canceled":
		return tracker.CheckFailure
	default:
		return tracker.CheckPending
	}
}

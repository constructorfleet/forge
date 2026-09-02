package gitlab

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Teagan42/forge/internal/tracker"
)

// glMergeRequest is the subset of GitLab's merge request JSON shape this
// package normalizes. Unexported: this shape never leaves the gitlab package
// (see CONTEXT.md "Tracker Adapter").
type glMergeRequest struct {
	IID          int    `json:"iid"`
	WebURL       string `json:"web_url"`
	State        string `json:"state"`
	TargetBranch string `json:"target_branch"`
	// DetailedMergeStatus is GitLab's one-reason-at-a-time merge diagnostic.
	// It is empty on an older GitLab instance and while GitLab still computes
	// the status.
	DetailedMergeStatus string `json:"detailed_merge_status"`
	// MergeStatus is the older, coarser field. It is the fallback diagnostic
	// when DetailedMergeStatus is empty.
	MergeStatus string `json:"merge_status"`
	// HasConflicts is the fallback conflict signal for an older instance.
	HasConflicts bool `json:"has_conflicts"`
	// DivergedCommitsCount is the fallback staleness signal for an older
	// instance. A count above zero means the source branch is behind.
	DivergedCommitsCount int `json:"diverged_commits_count"`
	// Pipeline is the merge request's head pipeline. It is nil when the
	// project ran no pipeline for the merge request yet.
	Pipeline *struct {
		Status string `json:"status"`
	} `json:"pipeline"`
}

// mergeRequestPath builds the "/projects/{project}/merge_requests/{iid}"
// path every merge-request-scoped endpoint shares, so that prefix is written
// once instead of at each call site.
func (c *Client) mergeRequestPath(iid int, suffix string) string {
	return fmt.Sprintf("%s/merge_requests/%d%s", c.projectPath(), iid, suffix)
}

// getMergeRequest fetches one merge request by its project-scoped iid.
func (c *Client) getMergeRequest(ctx context.Context, iid int) (glMergeRequest, error) {
	var mr glMergeRequest
	if err := c.do(ctx, http.MethodGet, c.mergeRequestPath(iid, ""), nil, &mr); err != nil {
		return glMergeRequest{}, err
	}
	return mr, nil
}

// mergeStatusOf normalizes one merge request into the neutral SCM merge
// status.
//
// GitLab reports detailed_merge_status on a current instance. An older
// instance, and a merge request GitLab still computes, leaves the field
// empty; the coarse merge_status, has_conflicts, and diverged_commits_count
// fields are the fallback for that case.
func mergeStatusOf(mr glMergeRequest) tracker.ChangeRequestMergeStatus {
	detail := mr.DetailedMergeStatus
	conflicted := detail == "conflict"
	behind := detail == "need_rebase"
	if detail == "" {
		detail = mr.MergeStatus
		conflicted = mr.HasConflicts
		behind = mr.DivergedCommitsCount > 0
	}
	return tracker.ChangeRequestMergeStatus{
		Merged:     mr.State == "merged",
		Conflicted: conflicted,
		Behind:     behind,
		RawDetail:  detail,
	}
}

// MapDetailedMergeStatus turns GitLab's detailed_merge_status string into one
// neutral merge blocker. It returns nil when nothing blocks the merge.
//
// GitLab reports one obstacle at a time, so this function returns at most one
// blocker. "mergeable" means nothing blocks the merge. An empty string means
// GitLab did not report the field, either because the instance is older or
// because GitLab still computes the status; an unreported field must not
// appear as a blocker, so it also returns nil.
//
// Forge maps only the reasons it acts on to a specific neutral reason. Every
// other reason maps to tracker.Blocked, and the raw GitLab string stays on
// RawDetail for a human reader.
func MapDetailedMergeStatus(reason string) *tracker.MergeBlocker {
	switch reason {
	case "mergeable", "":
		return nil
	case "ci_must_pass":
		return &tracker.MergeBlocker{Reason: tracker.ChecksFailing, Source: tracker.CapabilityCI, RawDetail: reason}
	case "ci_still_running":
		return &tracker.MergeBlocker{Reason: tracker.ChecksPending, Source: tracker.CapabilityCI, RawDetail: reason}
	case "not_approved":
		return &tracker.MergeBlocker{Reason: tracker.NotApproved, Source: tracker.CapabilitySCM, RawDetail: reason}
	case "conflict":
		return &tracker.MergeBlocker{Reason: tracker.Conflict, Source: tracker.CapabilitySCM, RawDetail: reason}
	case "need_rebase":
		return &tracker.MergeBlocker{Reason: tracker.Behind, Source: tracker.CapabilitySCM, RawDetail: reason}
	default:
		return &tracker.MergeBlocker{Reason: tracker.Blocked, Source: tracker.CapabilitySCM, RawDetail: reason}
	}
}

// EvaluateMergeEligibility reports GitLab's single mergeability reason as a
// neutral Forge verdict. GitLab already composes CI, approval, conflict, and
// branch state into detailed_merge_status, so this method reads one merge
// request and maps that reason without calling other endpoints.
func (c *Client) EvaluateMergeEligibility(ctx context.Context, ref tracker.ChangeRequestRef) (tracker.MergeEligibility, error) {
	mr, err := c.getMergeRequest(ctx, ref.Number)
	if err != nil {
		return tracker.MergeEligibility{}, err
	}

	if blocker := MapDetailedMergeStatus(mr.DetailedMergeStatus); blocker != nil {
		return tracker.MergeEligibility{Mergeable: false, Blockers: []tracker.MergeBlocker{*blocker}}, nil
	}
	return tracker.EvaluateMergeEligibility(tracker.MergeEligibilityInput{
		MergeStatus: mergeStatusOf(mr),
	}), nil
}

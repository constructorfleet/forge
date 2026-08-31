package github

import (
	"context"
	"errors"
	"net/http"
	"strconv"
)

// ghDependency is the subset of a GitHub issue-dependency relationship the
// adapter reads. The `/dependencies/blocked_by` subresource returns full
// issue objects; only the number crosses into the domain (see CONTEXT.md
// "Tracker Adapter").
type ghDependency struct {
	Number int `json:"number"`
}

// fetchBlockedBy reads number's native GitHub "blocked by" issue
// dependencies — the issues that must complete before this one — and
// returns them as Forge Issue IDs (decimal strings).
//
// Native relationships are the canonical GitHub Dependency Source (ADR
// 0003): when present they take precedence over the `## Dependencies` body
// block, which remains a fallback for repositories or GitHub hosts that do
// not expose the issue-dependencies API. A 404 from the subresource means
// the feature is unavailable there; fetchBlockedBy reports that via
// ok=false so the caller can fall back to the body block rather than fail.
// Any other error propagates — silently dropping a real dependency would
// let an Issue schedule as if it had no prerequisites, so the adapter fails
// closed rather than guessing.
func (c *Client) fetchBlockedBy(ctx context.Context, number int) (ids []string, ok bool, err error) {
	var deps []ghDependency
	// per_page=100 keeps all but pathological dependency counts to a single
	// page; the DAG at MVP scale never approaches it (see GetIssues on why
	// round-trip minimization is not the priority here).
	path := c.issuePath(number, "/dependencies/blocked_by?per_page=100")
	if e := c.do(ctx, http.MethodGet, path, nil, &deps); e != nil {
		var notFound *NotFoundError
		if errors.As(e, &notFound) {
			return nil, false, nil
		}
		return nil, false, e
	}
	ids = make([]string, 0, len(deps))
	for _, d := range deps {
		ids = append(ids, strconv.Itoa(d.Number))
	}
	return ids, true, nil
}

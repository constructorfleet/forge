package github

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Teagan42/forge/internal/tracker"
)

// ghPullRequestMergeability is the subset of GitHub's pull request JSON
// shape GetPullRequestMergeStatus normalizes.
type ghPullRequestMergeability struct {
	MergeableState string `json:"mergeable_state"`
	Merged         bool   `json:"merged"`
}

// GetPullRequestMergeStatus returns pull request number's current
// mergeability against its base branch. GitHub computes mergeable_state
// asynchronously after a push; "dirty" is the only value that
// unambiguously means "conflicts with the base branch" and "behind" is the
// only value that unambiguously means "stale relative to the base branch"
// — every other value (including "unknown", returned while GitHub is
// still computing) normalizes to both Conflicted: false and Behind: false
// rather than being guessed at, so a still-computing PR is never
// misreported as either.
func (c *Client) GetPullRequestMergeStatus(ctx context.Context, number int) (tracker.PullRequestMergeStatus, error) {
	var pr ghPullRequestMergeability
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", c.owner, c.repo, number)
	if err := c.do(ctx, http.MethodGet, path, nil, &pr); err != nil {
		return tracker.PullRequestMergeStatus{}, err
	}
	return tracker.PullRequestMergeStatus{
		Merged:     pr.Merged,
		Conflicted: pr.MergeableState == "dirty",
		Behind:     pr.MergeableState == "behind",
		RawDetail:  pr.MergeableState,
	}, nil
}

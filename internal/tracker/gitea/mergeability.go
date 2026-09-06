package gitea

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/tracker"
)

// GetPullRequestMergeStatus returns pull request number's current mergeability
// against its base branch.
//
// Gitea reports a coarse mergeable boolean and a merged boolean, not GitHub's
// mergeable_state string. GetPullRequestMergeStatus therefore reports
// Conflicted when the pull request is open and not mergeable, and never reports
// Behind: Gitea exposes no "behind the base branch" signal, so the rebase
// prompt that GitHub's "behind" state drives (issue 233) does not apply to a
// Gitea pull request. Gitea's mergeable field is coarser than GitHub's — it can
// be false for a reason other than a conflict — so RawDetail keeps both raw
// booleans for a human reader.
func (c *Client) GetPullRequestMergeStatus(ctx context.Context, number int) (tracker.PullRequestMergeStatus, error) {
	pr, err := c.getPullRequest(ctx, number)
	if err != nil {
		return tracker.PullRequestMergeStatus{}, err
	}
	return tracker.PullRequestMergeStatus{
		Merged:     pr.Merged,
		Conflicted: !pr.Merged && !pr.Mergeable,
		Behind:     false,
		RawDetail:  fmt.Sprintf("mergeable=%t merged=%t", pr.Mergeable, pr.Merged),
	}, nil
}

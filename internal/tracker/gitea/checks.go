package gitea

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Teagan42/forge/internal/tracker"
)

// giteaCombinedStatus is the subset of Gitea's combined-commit-status JSON
// shape GetPullRequestChecks normalizes. Gitea reports each CI result as a
// commit status, not as a separate check-run resource (GitHub's model), so the
// commit's combined status is the one source of a pull request's checks.
type giteaCombinedStatus struct {
	Statuses []struct {
		Context     string `json:"context"`
		Status      string `json:"status"`
		Description string `json:"description"`
	} `json:"statuses"`
}

// GetPullRequestChecks returns the normalized commit statuses attached to pull
// request number's head commit. Gitea's combined-status endpoint already
// reports the latest status per context, so no per-name merge is needed.
func (c *Client) GetPullRequestChecks(ctx context.Context, number int) ([]tracker.PullRequestCheck, error) {
	pr, err := c.getPullRequest(ctx, number)
	if err != nil {
		return nil, err
	}

	var combined giteaCombinedStatus
	path := fmt.Sprintf("%s/commits/%s/status", c.repoPath(), pr.Head.SHA)
	if err := c.do(ctx, http.MethodGet, path, nil, &combined); err != nil {
		return nil, err
	}

	out := make([]tracker.PullRequestCheck, 0, len(combined.Statuses))
	for _, status := range combined.Statuses {
		out = append(out, tracker.PullRequestCheck{
			Name:    status.Context,
			State:   normalizeStatusState(status.Status),
			Details: status.Description,
		})
	}
	return out, nil
}

// normalizeStatusState maps a Gitea commit-status state onto the neutral check
// state. "success" is the only state that passes. "failure" and "error" are the
// only states that fail. Every other state, including "pending", "warning", and
// one Forge does not know, is pending, so a status that still runs is never
// misreported as done.
func normalizeStatusState(state string) tracker.CheckState {
	switch state {
	case "success":
		return tracker.CheckSuccess
	case "failure", "error":
		return tracker.CheckFailure
	default:
		return tracker.CheckPending
	}
}

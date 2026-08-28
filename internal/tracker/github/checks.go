package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Teagan42/forge/internal/tracker"
)

type ghPullRequestHead struct {
	Head struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type ghCombinedStatus struct {
	Statuses []struct {
		Context     string `json:"context"`
		State       string `json:"state"`
		Description string `json:"description"`
	} `json:"statuses"`
}

type ghCheckRuns struct {
	CheckRuns []struct {
		Name       string  `json:"name"`
		Status     string  `json:"status"`
		Conclusion *string `json:"conclusion"`
		Output     struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
		} `json:"output"`
	} `json:"check_runs"`
}

// GetPullRequestChecks returns the normalized statuses and check runs
// attached to pull request number's head commit.
func (c *Client) GetPullRequestChecks(ctx context.Context, number int) ([]tracker.PullRequestCheck, error) {
	var pr ghPullRequestHead
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/pulls/%d", c.owner, c.repo, number), nil, &pr); err != nil {
		return nil, err
	}

	checks := map[string]tracker.PullRequestCheck{}

	var statuses ghCombinedStatus
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/commits/%s/status", c.owner, c.repo, pr.Head.SHA), nil, &statuses); err != nil {
		return nil, err
	}
	for _, status := range statuses.Statuses {
		mergeCheck(checks, tracker.PullRequestCheck{
			Name:    status.Context,
			State:   normalizeStatusState(status.State),
			Details: status.Description,
		})
	}

	var runs ghCheckRuns
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs", c.owner, c.repo, pr.Head.SHA), nil, &runs); err != nil {
		return nil, err
	}
	for _, run := range runs.CheckRuns {
		mergeCheck(checks, tracker.PullRequestCheck{
			Name:    run.Name,
			State:   normalizeCheckRunState(run.Status, run.Conclusion),
			Details: strings.TrimSpace(strings.TrimSpace(run.Output.Title) + "\n" + strings.TrimSpace(run.Output.Summary)),
		})
	}

	out := make([]tracker.PullRequestCheck, 0, len(checks))
	for _, check := range checks {
		out = append(out, check)
	}
	return out, nil
}

func mergeCheck(into map[string]tracker.PullRequestCheck, incoming tracker.PullRequestCheck) {
	if current, ok := into[incoming.Name]; ok {
		if checkPriority(incoming.State) < checkPriority(current.State) {
			return
		}
		if current.Details != "" && incoming.Details == "" {
			incoming.Details = current.Details
		}
	}
	into[incoming.Name] = incoming
}

func checkPriority(state tracker.CheckState) int {
	switch state {
	case tracker.CheckFailure:
		return 3
	case tracker.CheckPending:
		return 2
	default:
		return 1
	}
}

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

func normalizeCheckRunState(status string, conclusion *string) tracker.CheckState {
	if status != "completed" || conclusion == nil {
		return tracker.CheckPending
	}
	switch *conclusion {
	case "success", "neutral", "skipped":
		return tracker.CheckSuccess
	case "failure", "timed_out", "cancelled", "action_required", "startup_failure", "stale":
		return tracker.CheckFailure
	default:
		return tracker.CheckPending
	}
}

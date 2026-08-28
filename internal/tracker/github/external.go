package github

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Teagan42/forge/internal/tracker"
)

// GitReachabilityChecker reports whether commit is reachable from (an
// ancestor of) the current tip of branch in the local git checkout.
// Injected on Client so CheckExternal's GitHub-API concerns (testable via
// httptest, see external_test.go) stay separate from git reachability
// (testable with a fake here; cmd/forge wires a production implementation
// that shells out to `git merge-base --is-ancestor`).
type GitReachabilityChecker interface {
	IsAncestor(ctx context.Context, commit, branch string) (bool, error)
}

// ghIssueState is the minimal shape CheckExternal needs from GitHub's issue
// JSON: whether the issue is open or closed (CONTEXT.md "External Issue":
// closed does not equal satisfied).
type ghIssueState struct {
	State string `json:"state"`
}

// ghTimelineEvent is the subset of GitHub's issue-timeline JSON shape
// CheckExternal uses to find Pull Requests that reference (and, per
// GitHub's own closing-keyword linking, are intended to close) the Issue.
// A "cross-referenced" event whose source is a Pull Request is GitHub's
// normalized signal for "this PR references this issue" — the closest
// thing GitHub's REST API exposes to reverse issue->PR linkage.
type ghTimelineEvent struct {
	Event  string `json:"event"`
	Source *struct {
		Issue *struct {
			Number      int `json:"number"`
			PullRequest *struct {
				MergedAt *string `json:"merged_at"`
			} `json:"pull_request"`
		} `json:"issue"`
	} `json:"source"`
}

// ghPullRequest is the subset of GitHub's pull-request JSON shape
// CheckExternal needs: whether it merged, and if so the merge commit to
// check reachability of.
type ghPullRequest struct {
	MergedAt       *string `json:"merged_at"`
	MergeCommitSHA string  `json:"merge_commit_sha"`
}

var _ tracker.ExternalChecker = (*Client)(nil)

// CheckExternal implements tracker.ExternalChecker: it determines issueID's
// current tracker.ExternalState by finding any Pull Request that
// cross-references it and has merged, then (via Reachability) checking
// whether that PR's merge commit is reachable from baseBranch.
//
//   - A merged PR whose merge commit is reachable from baseBranch ->
//     ExternalSatisfied.
//   - A merged PR whose merge commit is not (yet) reachable, or no merged
//     PR at all while the issue remains open -> ExternalPending (may still
//     become satisfied later; callers should recheck rather than treat
//     this as final).
//   - No merged PR while the issue is closed -> ExternalInvalid: closed
//     does not equal satisfied (CONTEXT.md "External Issue", ADR 0008).
//
// CheckExternal makes no attempt to cache its answer across calls — every
// call re-fetches from GitHub and re-checks git reachability, which is
// what lets a later poll or `forge resume`/`forge execute` re-invocation
// observe newly-landed merges.
func (c *Client) CheckExternal(ctx context.Context, issueID, baseBranch string) (tracker.ExternalState, error) {
	number, err := parseIssueID(issueID)
	if err != nil {
		return "", err
	}

	var issue ghIssueState
	if err := c.do(ctx, http.MethodGet, c.issuePath(number, ""), nil, &issue); err != nil {
		return "", fmt.Errorf("github: check external issue %s: %w", issueID, err)
	}

	mergeSHA, found, err := c.findMergedPRCommit(ctx, number)
	if err != nil {
		return "", fmt.Errorf("github: check external issue %s: %w", issueID, err)
	}

	if !found {
		if issue.State == "closed" {
			return tracker.ExternalInvalid, nil
		}
		return tracker.ExternalPending, nil
	}

	if c.Reachability == nil {
		return "", fmt.Errorf(
			"github: check external issue %s: found merged PR but no GitReachabilityChecker is configured", issueID)
	}
	reachable, err := c.Reachability.IsAncestor(ctx, mergeSHA, baseBranch)
	if err != nil {
		return "", fmt.Errorf("github: check external issue %s: reachability check for %s against %s: %w",
			issueID, mergeSHA, baseBranch, err)
	}
	if !reachable {
		return tracker.ExternalPending, nil
	}
	return tracker.ExternalSatisfied, nil
}

// findMergedPRCommit walks issue #number's timeline looking for a
// cross-referenced Pull Request that has merged, returning its merge
// commit SHA. If more than one merged PR references the issue, the first
// one found (GitHub's timeline order) wins. found is false if no merged PR
// was located (the issue may still have an open PR referencing it, or
// none at all).
func (c *Client) findMergedPRCommit(ctx context.Context, number int) (sha string, found bool, err error) {
	var events []ghTimelineEvent
	path := c.issuePath(number, "/timeline") + "?per_page=100"
	if err := c.do(ctx, http.MethodGet, path, nil, &events); err != nil {
		return "", false, fmt.Errorf("fetch timeline for issue #%d: %w", number, err)
	}

	for _, ev := range events {
		if ev.Event != "cross-referenced" || ev.Source == nil || ev.Source.Issue == nil {
			continue
		}
		src := ev.Source.Issue
		if src.PullRequest == nil || src.PullRequest.MergedAt == nil {
			continue
		}

		var pr ghPullRequest
		prPath := fmt.Sprintf("/repos/%s/%s/pulls/%d", c.owner, c.repo, src.Number)
		if err := c.do(ctx, http.MethodGet, prPath, nil, &pr); err != nil {
			return "", false, fmt.Errorf("fetch pull request #%d: %w", src.Number, err)
		}
		if pr.MergedAt != nil && pr.MergeCommitSHA != "" {
			return pr.MergeCommitSHA, true, nil
		}
	}
	return "", false, nil
}

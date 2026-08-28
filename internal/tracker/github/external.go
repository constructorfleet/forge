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
// CheckExternal uses. Two event kinds matter:
//
//   - "cross-referenced": GitHub's signal that some other Issue/PR
//     references this one. This fires for ANY mention (e.g. "related to
//     #50"), not just the PR that actually closes the issue — so it is
//     only a candidate list, never proof of association by itself (see
//     findMergedPRCommit).
//   - "closed": GitHub's authoritative record of what closed the issue.
//     When closed by a commit (directly, or via a merged PR's merge
//     commit), CommitID is that commit's SHA — the one signal that
//     unambiguously ties a specific commit to this issue's closure.
type ghTimelineEvent struct {
	Event    string `json:"event"`
	CommitID string `json:"commit_id"`
	Source   *struct {
		Issue *struct {
			Number      int `json:"number"`
			PullRequest *struct {
				MergedAt *string `json:"merged_at"`
			} `json:"pull_request"`
		} `json:"issue"`
	} `json:"source"`
}

// ghMergedPullRequest is the subset of GitHub's pull-request JSON shape
// CheckExternal needs: whether it merged, and if so the merge commit to
// check reachability of.
type ghMergedPullRequest struct {
	MergedAt       *string `json:"merged_at"`
	MergeCommitSHA string  `json:"merge_commit_sha"`
}

var _ tracker.ExternalChecker = (*Client)(nil)

// CheckExternal implements tracker.ExternalChecker: it determines issueID's
// current tracker.ExternalState by finding the Pull Request that
// authoritatively closed it (see findMergedPRCommit — not merely any PR
// that references it), then (via Reachability) checking whether that PR's
// merge commit is reachable from baseBranch.
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

// findMergedPRCommit walks issue #number's full timeline (following every
// page, see fetchTimeline) to find the merged Pull Request that
// authoritatively closed it, returning its merge commit SHA. found is
// false if no such PR was located — the issue may still be open, closed by
// a direct commit with no associated PR, or a "cross-referenced" PR merely
// mentions it without being the one that closed it.
//
// A "cross-referenced" event fires for any mention of the issue, so it is
// only ever a *candidate*: a merged PR that references the issue is not
// necessarily the PR that closed it (see ghTimelineEvent's doc comment).
// The timeline's "closed" event is the authoritative signal — its
// CommitID is the exact commit that closed the issue — so a candidate PR
// only counts once its merge commit matches that CommitID.
func (c *Client) findMergedPRCommit(ctx context.Context, number int) (sha string, found bool, err error) {
	events, err := c.fetchTimeline(ctx, number)
	if err != nil {
		return "", false, err
	}

	closingCommit := closingCommitSHA(events)
	if closingCommit == "" {
		return "", false, nil
	}

	for _, ev := range events {
		if ev.Event != "cross-referenced" || ev.Source == nil || ev.Source.Issue == nil {
			continue
		}
		src := ev.Source.Issue
		if src.PullRequest == nil || src.PullRequest.MergedAt == nil {
			continue
		}

		var pr ghMergedPullRequest
		prPath := fmt.Sprintf("/repos/%s/%s/pulls/%d", c.owner, c.repo, src.Number)
		if err := c.do(ctx, http.MethodGet, prPath, nil, &pr); err != nil {
			return "", false, fmt.Errorf("fetch pull request #%d: %w", src.Number, err)
		}
		// Re-confirm against the authoritative PR resource: the timeline
		// cross-reference's merged_at can be stale by the time we look.
		if pr.MergedAt == nil || pr.MergeCommitSHA == "" {
			continue
		}
		if pr.MergeCommitSHA == closingCommit {
			return pr.MergeCommitSHA, true, nil
		}
	}
	return "", false, nil
}

// closingCommitSHA returns the commit that most recently closed the issue
// according to events, or "" if the issue has never been closed by a
// commit (still open, or closed manually with no associated commit — e.g.
// "close as not planned"). A "reopened" event clears any prior closing
// commit, so a since-reopened-and-not-yet-reclosed issue correctly yields
// "".
func closingCommitSHA(events []ghTimelineEvent) string {
	var sha string
	for _, ev := range events {
		switch ev.Event {
		case "closed":
			sha = ev.CommitID
		case "reopened":
			sha = ""
		}
	}
	return sha
}

// fetchTimeline fetches every page of issue #number's timeline, following
// the Link "next" header the same way GetComments does — a long-lived
// issue's timeline (labels, comments, cross-references, ...) can easily
// exceed one page, and the event that matters (the authoritative "closed"
// event, or the PR that references it) can land on any page.
func (c *Client) fetchTimeline(ctx context.Context, number int) ([]ghTimelineEvent, error) {
	url := c.baseURL + c.issuePath(number, "/timeline") + "?per_page=100"

	var events []ghTimelineEvent
	for url != "" {
		var page []ghTimelineEvent
		headers, err := c.doWithHeaders(ctx, http.MethodGet, url, nil, &page)
		if err != nil {
			return nil, fmt.Errorf("fetch timeline for issue #%d: %w", number, err)
		}
		events = append(events, page...)
		url = nextPageURL(headers)
	}
	return events, nil
}

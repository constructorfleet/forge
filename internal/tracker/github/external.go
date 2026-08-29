package github

import (
	"context"
	"fmt"

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

// closingPRsQuery asks GitHub which merged Pull Request(s) authoritatively
// closed an issue, alongside the issue's own open/closed state. This is the
// one association that survives every merge strategy: closedByPullRequestsReferences
// is populated whether the closing PR was created with a merge commit,
// squash-merged, or rebase-merged, whereas the REST timeline's "closed"
// event carries a commit_id only for merge-commit merges (it is null for
// squash/rebase — the very strategy this repository uses), which is why the
// previous timeline-commit_id association misclassified every squash-merged
// closer as "no merged PR" and rendered the dependency permanently
// unsatisfiable.
//
// includeClosedPrs:true is required because a merged PR is a closed PR;
// without it the field only reports still-open PRs that *will* close the
// issue.
const closingPRsQuery = `query($owner:String!,$repo:String!,$number:Int!){` +
	`repository(owner:$owner,name:$repo){` +
	`issue(number:$number){state ` +
	`closedByPullRequestsReferences(first:10,includeClosedPrs:true){` +
	`nodes{merged mergeCommit{oid}}}}}}`

var _ tracker.ExternalChecker = (*Client)(nil)

// CheckExternal implements tracker.ExternalChecker: it determines issueID's
// current tracker.ExternalState by finding the Pull Request that
// authoritatively closed it (via GitHub's closedByPullRequestsReferences —
// not merely any PR that references it), then (via Reachability) checking
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
// call re-queries GitHub and re-checks git reachability, which is what lets
// a later poll or `forge resume`/`forge execute` re-invocation observe
// newly-landed merges.
func (c *Client) CheckExternal(ctx context.Context, issueID, baseBranch string) (tracker.ExternalState, error) {
	number, err := parseIssueID(issueID)
	if err != nil {
		return "", err
	}

	closed, mergeSHA, found, err := c.closingMergedPR(ctx, number)
	if err != nil {
		return "", fmt.Errorf("github: check external issue %s: %w", issueID, err)
	}

	if !found {
		if closed {
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

// closingMergedPR queries closedByPullRequestsReferences for issue #number
// and returns whether the issue is closed, plus the merge commit SHA of the
// merged Pull Request that closed it. found is false when no *merged* closing
// PR exists — the issue may still be open, closed by a direct commit with no
// PR, or closed with only an unmerged PR referencing it (all of which leave
// no merge commit whose reachability could satisfy the dependency).
//
// When GitHub reports more than one closing PR (an issue can be closed,
// reopened, and closed again), the first merged one with a recorded merge
// commit is authoritative enough for satisfaction: reachability of any
// merged closer's commit from the base means that work has landed.
func (c *Client) closingMergedPR(ctx context.Context, number int) (closed bool, sha string, found bool, err error) {
	var resp struct {
		Repository struct {
			Issue struct {
				State                          string `json:"state"`
				ClosedByPullRequestsReferences struct {
					Nodes []struct {
						Merged      bool `json:"merged"`
						MergeCommit *struct {
							OID string `json:"oid"`
						} `json:"mergeCommit"`
					} `json:"nodes"`
				} `json:"closedByPullRequestsReferences"`
			} `json:"issue"`
		} `json:"repository"`
	}
	vars := map[string]interface{}{"owner": c.owner, "repo": c.repo, "number": number}
	if err := c.graphQL(ctx, closingPRsQuery, vars, &resp); err != nil {
		return false, "", false, err
	}

	issue := resp.Repository.Issue
	// GraphQL reports issue state in upper case ("OPEN"/"CLOSED"), unlike the
	// REST API's lower case.
	closed = issue.State == "CLOSED"
	for _, n := range issue.ClosedByPullRequestsReferences.Nodes {
		if n.Merged && n.MergeCommit != nil && n.MergeCommit.OID != "" {
			return closed, n.MergeCommit.OID, true, nil
		}
	}
	return closed, "", false, nil
}

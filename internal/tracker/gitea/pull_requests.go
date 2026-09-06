package gitea

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Teagan42/forge/internal/tracker"
)

// giteaPullRequest is the subset of Gitea's pull request JSON shape Client
// normalizes. Unexported: this shape never leaves the gitea package (see
// CONTEXT.md "Tracker Adapter").
type giteaPullRequest struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Base    struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Mergeable bool `json:"mergeable"`
	Merged    bool `json:"merged"`
}

// CreatePullRequest idempotently creates a pull request from req.Head into
// req.Base. It first queries Gitea for an existing open pull request whose head
// branch is req.Head — if one is found, it is recovered (returned) rather than
// duplicated. As a belt-and-suspenders guard against a race between that check
// and the create call (e.g. two forge processes retrying concurrently), a 409
// or 422 from the create call itself is also treated as "already exists" and
// triggers the same recovery lookup rather than propagating as a hard failure.
func (c *Client) CreatePullRequest(ctx context.Context, req tracker.PullRequestRequest) (tracker.PullRequest, error) {
	if existing, ok, err := c.findOpenPullRequest(ctx, req.Head); err != nil {
		return tracker.PullRequest{}, err
	} else if ok {
		return existing, nil
	}

	reqBody := struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body"`
	}{Title: req.Title, Head: req.Head, Base: req.Base, Body: req.Body}

	var resp giteaPullRequest
	if err := c.do(ctx, http.MethodPost, c.repoPath()+"/pulls", reqBody, &resp); err != nil {
		var conflictErr *ConflictError
		var validationErr *ValidationError
		if errors.As(err, &conflictErr) || errors.As(err, &validationErr) {
			if existing, ok, findErr := c.findOpenPullRequest(ctx, req.Head); findErr == nil && ok {
				return existing, nil
			}
		}
		return tracker.PullRequest{}, err
	}
	return tracker.PullRequest{Number: resp.Number, URL: resp.HTMLURL}, nil
}

// findOpenPullRequest looks up an existing open pull request whose head branch
// is head, returning ok=false (and no error) if none exists.
//
// Gitea's pull-request list endpoint has no head-branch filter (unlike
// GitHub's head=owner:branch query), so findOpenPullRequest lists the open
// pull requests and matches head.ref itself. It follows the Link "next" header
// across every page so a repository with many open pull requests still finds
// the match.
func (c *Client) findOpenPullRequest(ctx context.Context, head string) (tracker.PullRequest, bool, error) {
	url := c.baseURL + c.repoPath() + "/pulls?state=open&limit=50"
	for url != "" {
		var prs []giteaPullRequest
		headers, err := c.doWithHeaders(ctx, http.MethodGet, url, nil, &prs)
		if err != nil {
			return tracker.PullRequest{}, false, err
		}
		for _, pr := range prs {
			if pr.Head.Ref == head {
				return tracker.PullRequest{Number: pr.Number, URL: pr.HTMLURL}, true, nil
			}
		}
		url = nextPageURL(headers)
	}
	return tracker.PullRequest{}, false, nil
}

// GetPullRequestTargetBranch returns pull request number's current target
// branch. Gitea can retarget a stacked pull request after its base merges.
func (c *Client) GetPullRequestTargetBranch(ctx context.Context, number int) (string, error) {
	pr, err := c.getPullRequest(ctx, number)
	if err != nil {
		return "", err
	}
	return pr.Base.Ref, nil
}

// getPullRequest fetches one pull request by its repository-scoped index.
func (c *Client) getPullRequest(ctx context.Context, number int) (giteaPullRequest, error) {
	var pr giteaPullRequest
	path := fmt.Sprintf("%s/pulls/%d", c.repoPath(), number)
	if err := c.do(ctx, http.MethodGet, path, nil, &pr); err != nil {
		return giteaPullRequest{}, err
	}
	return pr, nil
}

package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/Teagan42/forge/internal/tracker"
)

// ghPullRequest is the subset of GitHub's pull request JSON shape Client
// normalizes. Unexported: this shape never leaves the github package (see
// CONTEXT.md "Tracker Adapter").
type ghPullRequest struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Base    struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

// CreatePullRequest idempotently creates a pull request from req.Head into
// req.Base. It first queries GitHub for an existing open pull request
// against req.Head — if one is found, it is recovered (returned) rather
// than duplicated. As a belt-and-suspenders guard against a race between
// that check and the create call (e.g. two forge processes retrying
// concurrently), a 422 from the create call itself is also treated as
// "already exists" and triggers the same recovery lookup rather than
// propagating as a hard failure.
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

	var resp ghPullRequest
	path := fmt.Sprintf("/repos/%s/%s/pulls", c.owner, c.repo)
	if err := c.do(ctx, http.MethodPost, path, reqBody, &resp); err != nil {
		var validationErr *ValidationError
		if errors.As(err, &validationErr) {
			if existing, ok, findErr := c.findOpenPullRequest(ctx, req.Head); findErr == nil && ok {
				return existing, nil
			}
		}
		return tracker.PullRequest{}, err
	}
	return tracker.PullRequest{Number: resp.Number, URL: resp.HTMLURL}, nil
}

// findOpenPullRequest looks up an existing open pull request whose head
// branch is head, returning ok=false (and no error) if none exists.
func (c *Client) findOpenPullRequest(ctx context.Context, head string) (tracker.PullRequest, bool, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=open&head=%s:%s",
		c.owner, c.repo, url.QueryEscape(c.owner), url.QueryEscape(head))

	var prs []ghPullRequest
	if err := c.do(ctx, http.MethodGet, path, nil, &prs); err != nil {
		return tracker.PullRequest{}, false, err
	}
	if len(prs) == 0 {
		return tracker.PullRequest{}, false, nil
	}
	return tracker.PullRequest{Number: prs[0].Number, URL: prs[0].HTMLURL}, true, nil
}

// GetPullRequestTargetBranch returns pull request number's current target
// branch. GitHub can retarget stacked pull requests after their base merges.
func (c *Client) GetPullRequestTargetBranch(ctx context.Context, number int) (string, error) {
	var pr ghPullRequest
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", c.owner, c.repo, number)
	if err := c.do(ctx, http.MethodGet, path, nil, &pr); err != nil {
		return "", err
	}
	return pr.Base.Ref, nil
}

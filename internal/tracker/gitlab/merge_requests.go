package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/Teagan42/forge/internal/tracker"
)

// createMergeRequest idempotently creates a merge request from req.Head into
// req.Base. It first asks GitLab for an open merge request between the same
// two branches. If one exists, it recovers that merge request and creates
// nothing.
func (c *Client) createMergeRequest(ctx context.Context, req tracker.ChangeRequestRequest) (tracker.ChangeRequest, error) {
	if existing, ok, err := c.findOpenMergeRequest(ctx, req.Head, req.Base); err != nil {
		return tracker.ChangeRequest{}, err
	} else if ok {
		return existing, nil
	}

	body := struct {
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		Title        string `json:"title"`
		Description  string `json:"description"`
	}{SourceBranch: req.Head, TargetBranch: req.Base, Title: req.Title, Description: req.Body}

	var created glMergeRequest
	if err := c.do(ctx, http.MethodPost, c.projectPath()+"/merge_requests", body, &created); err != nil {
		// Two Forge processes can race between the lookup above and this
		// create call. GitLab then rejects the second create with 409, and an
		// older instance rejects it with 400 or 422. Look the merge request up
		// once more and recover it. Report the original failure if nothing is
		// recoverable, so a genuinely bad request stays a failure.
		var conflictErr *ConflictError
		var validationErr *ValidationError
		if errors.As(err, &conflictErr) || errors.As(err, &validationErr) {
			if existing, ok, findErr := c.findOpenMergeRequest(ctx, req.Head, req.Base); findErr == nil && ok {
				return existing, nil
			}
		}
		return tracker.ChangeRequest{}, err
	}
	return c.changeRequestFrom(created), nil
}

// findOpenMergeRequest looks up an open merge request from head into base. It
// returns ok=false, and no error, when no such merge request exists.
func (c *Client) findOpenMergeRequest(ctx context.Context, head, base string) (tracker.ChangeRequest, bool, error) {
	path := fmt.Sprintf("%s/merge_requests?source_branch=%s&target_branch=%s&state=opened",
		c.projectPath(), url.QueryEscape(head), url.QueryEscape(base))

	var found []glMergeRequest
	if err := c.do(ctx, http.MethodGet, path, nil, &found); err != nil {
		return tracker.ChangeRequest{}, false, err
	}
	if len(found) == 0 {
		return tracker.ChangeRequest{}, false, nil
	}
	return c.changeRequestFrom(found[0]), true, nil
}

// changeRequestFrom turns a GitLab merge request into the neutral change
// request. GitLab numbers a merge request per project, so the neutral Number
// carries the project-scoped iid, not the instance-wide id.
func (c *Client) changeRequestFrom(mr glMergeRequest) tracker.ChangeRequest {
	return tracker.ChangeRequest{
		Ref: tracker.ChangeRequestRef{Provider: c.providerID(), Number: mr.IID},
		URL: mr.WebURL,
	}
}

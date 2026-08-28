package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// AddLabel idempotently ensures label is set on the Issue. GitHub's "add
// labels" endpoint is natively idempotent — POSTing a label that is
// already present succeeds without error — so no pre-check is needed.
func (c *Client) AddLabel(ctx context.Context, id string, label string) error {
	number, err := parseIssueID(id)
	if err != nil {
		return err
	}

	reqBody := struct {
		Labels []string `json:"labels"`
	}{Labels: []string{label}}

	path := fmt.Sprintf("/repos/%s/%s/issues/%d/labels", c.owner, c.repo, number)
	return c.do(ctx, http.MethodPost, path, reqBody, nil)
}

// RemoveLabel idempotently ensures label is not set on the Issue. GitHub's
// "remove label" endpoint 404s when the label is already absent; that
// response is treated as success rather than an error so repeated calls
// are safe.
func (c *Client) RemoveLabel(ctx context.Context, id string, label string) error {
	number, err := parseIssueID(id)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/repos/%s/%s/issues/%d/labels/%s", c.owner, c.repo, number, url.PathEscape(label))
	err = c.do(ctx, http.MethodDelete, path, nil, nil)

	var notFound *NotFoundError
	if errors.As(err, &notFound) {
		return nil
	}
	return err
}

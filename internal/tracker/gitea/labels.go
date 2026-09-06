package gitea

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// giteaLabel is the subset of Gitea's label JSON shape Client normalizes.
// Gitea addresses a label on an issue by its numeric ID, not by its name, so
// the adapter must resolve a name to an ID before it adds or removes a label.
type giteaLabel struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// AddLabel idempotently ensures label is set on the Issue. Gitea's add-labels
// endpoint addresses a label by its numeric ID, unlike GitHub, which addresses
// it by name. AddLabel therefore resolves the label name to its ID through the
// repository's label list first, then posts that ID. Gitea's endpoint is
// natively idempotent — posting a label that is already present succeeds — so
// no pre-check of the issue's current labels is needed. A label name that the
// repository does not define is an error: Gitea cannot add a label that does
// not exist.
func (c *Client) AddLabel(ctx context.Context, id string, label string) error {
	number, err := parseIssueID(id)
	if err != nil {
		return err
	}

	labelID, found, err := c.labelID(ctx, label)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("gitea: cannot add label %q: the repository %s/%s defines no such label", label, c.owner, c.repo)
	}

	reqBody := struct {
		Labels []int64 `json:"labels"`
	}{Labels: []int64{labelID}}

	return c.do(ctx, http.MethodPost, c.issuePath(number, "/labels"), reqBody, nil)
}

// RemoveLabel idempotently ensures label is not set on the Issue. Gitea's
// remove-label endpoint addresses a label by its numeric ID, so RemoveLabel
// resolves the name to an ID first. A name the repository does not define means
// there is nothing to remove, so RemoveLabel reports success. A 404 from the
// delete call (the label is not on the issue) is treated the same way, so
// repeated calls are safe.
func (c *Client) RemoveLabel(ctx context.Context, id string, label string) error {
	number, err := parseIssueID(id)
	if err != nil {
		return err
	}

	labelID, found, err := c.labelID(ctx, label)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	path := c.issuePath(number, fmt.Sprintf("/labels/%d", labelID))
	err = c.do(ctx, http.MethodDelete, path, nil, nil)

	var notFound *NotFoundError
	if errors.As(err, &notFound) {
		return nil
	}
	return err
}

// labelID resolves a label name to its numeric ID by listing the repository's
// labels. found is false, with no error, when the repository defines no label
// with that name. It follows the Link "next" header across every page so a
// repository with many labels resolves correctly.
func (c *Client) labelID(ctx context.Context, name string) (id int64, found bool, err error) {
	url := c.baseURL + c.repoPath() + "/labels?limit=50"
	for url != "" {
		var page []giteaLabel
		headers, e := c.doWithHeaders(ctx, http.MethodGet, url, nil, &page)
		if e != nil {
			return 0, false, e
		}
		for _, l := range page {
			if l.Name == name {
				return l.ID, true, nil
			}
		}
		url = nextPageURL(headers)
	}
	return 0, false, nil
}

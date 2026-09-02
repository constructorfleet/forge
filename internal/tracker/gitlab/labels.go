package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// AddLabel idempotently makes sure label is set on the Issue. GitLab has no
// separate label endpoint for an issue: it updates labels through the issue
// update endpoint, with the "add_labels" parameter. That parameter is
// idempotent — a label that is already present stays present and GitLab
// reports success — so no pre-check is needed.
func (c *Client) AddLabel(ctx context.Context, id string, label string) error {
	iid, err := parseIssueID(id)
	if err != nil {
		return err
	}
	if err := checkLabel(label); err != nil {
		return err
	}

	reqBody := struct {
		AddLabels string `json:"add_labels"`
	}{AddLabels: label}

	return c.do(ctx, http.MethodPut, c.issuePath(iid, ""), reqBody, nil)
}

// RemoveLabel idempotently makes sure label is not set on the Issue, through
// the same issue update endpoint with the "remove_labels" parameter. GitLab
// already treats "remove_labels" as idempotent for an absent label: it
// silently ignores a label name that the Issue does not carry, and does not
// answer 404 for that case. A 404 from this endpoint means the Issue itself
// is not found, so RemoveLabel lets it propagate as a real error instead of
// swallowing it.
func (c *Client) RemoveLabel(ctx context.Context, id string, label string) error {
	iid, err := parseIssueID(id)
	if err != nil {
		return err
	}
	if err := checkLabel(label); err != nil {
		return err
	}

	reqBody := struct {
		RemoveLabels string `json:"remove_labels"`
	}{RemoveLabels: label}

	return c.do(ctx, http.MethodPut, c.issuePath(iid, ""), reqBody, nil)
}

// checkLabel rejects a label that holds a comma. GitLab reads the
// "add_labels" and "remove_labels" parameters as a comma-separated list, so
// such a label would become two labels. The adapter fails loudly rather than
// apply something the caller did not ask for.
func checkLabel(label string) error {
	if strings.Contains(label, ",") {
		return fmt.Errorf("gitlab: invalid label %q: a label must not hold a comma, because GitLab reads a comma as a label separator", label)
	}
	return nil
}

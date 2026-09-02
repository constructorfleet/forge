package linear

import (
	"context"
	"fmt"
)

const findLabelQuery = `
query($teamId: String!, $name: String!) {
  issueLabels(filter: { team: { id: { eq: $teamId } }, name: { eq: $name } }) {
    nodes { id name }
  }
}`

const createLabelMutation = `
mutation($teamId: String!, $name: String!) {
  issueLabelCreate(input: { teamId: $teamId, name: $name }) {
    success
    issueLabel { id }
  }
}`

// resolveLabelID finds the team-scoped IssueLabel id for name, creating the
// label on the configured team if it does not already exist. Linear
// attaches labels by id, not by name, so every AddLabel/RemoveLabel call
// resolves through this first.
func (c *Client) resolveLabelID(ctx context.Context, name string) (string, error) {
	teamID, err := c.resolveTeamID(ctx)
	if err != nil {
		return "", err
	}

	var found struct {
		IssueLabels struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		} `json:"issueLabels"`
	}
	vars := map[string]interface{}{"teamId": teamID, "name": name}
	if err := c.graphQL(ctx, findLabelQuery, vars, &found); err != nil {
		return "", fmt.Errorf("linear: find label %q: %w", name, err)
	}
	if len(found.IssueLabels.Nodes) > 0 {
		return found.IssueLabels.Nodes[0].ID, nil
	}

	var created struct {
		IssueLabelCreate struct {
			Success    bool `json:"success"`
			IssueLabel struct {
				ID string `json:"id"`
			} `json:"issueLabel"`
		} `json:"issueLabelCreate"`
	}
	if err := c.graphQL(ctx, createLabelMutation, vars, &created); err != nil {
		return "", fmt.Errorf("linear: create label %q: %w", name, err)
	}
	if !created.IssueLabelCreate.Success {
		return "", fmt.Errorf("linear: create label %q: request was not accepted", name)
	}
	return created.IssueLabelCreate.IssueLabel.ID, nil
}

const getIssueLabelIDsQuery = `
query($id: String!) {
  issue(id: $id) {
    labels {
      nodes { id }
    }
  }
}`

// issueLabelIDs fetches the label ids currently set on the issue at
// internalID.
func (c *Client) issueLabelIDs(ctx context.Context, internalID string) ([]string, error) {
	var out struct {
		Issue struct {
			Labels struct {
				Nodes []struct {
					ID string `json:"id"`
				} `json:"nodes"`
			} `json:"labels"`
		} `json:"issue"`
	}
	if err := c.graphQL(ctx, getIssueLabelIDsQuery, map[string]interface{}{"id": internalID}, &out); err != nil {
		return nil, err
	}
	ids := make([]string, len(out.Issue.Labels.Nodes))
	for i, n := range out.Issue.Labels.Nodes {
		ids[i] = n.ID
	}
	return ids, nil
}

const setIssueLabelIDsMutation = `
mutation($id: String!, $labelIds: [String!]!) {
  issueUpdate(id: $id, input: { labelIds: $labelIds }) {
    success
  }
}`

// setIssueLabelIDs replaces the full label set on the issue at internalID.
// Linear's issueUpdate takes the complete labelIds array, not an
// add/remove delta, so every AddLabel/RemoveLabel call reads the current
// set first and writes the new full set back.
func (c *Client) setIssueLabelIDs(ctx context.Context, internalID string, labelIDs []string) error {
	var out struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	ids := make([]interface{}, len(labelIDs))
	for i, id := range labelIDs {
		ids[i] = id
	}
	vars := map[string]interface{}{"id": internalID, "labelIds": ids}
	if err := c.graphQL(ctx, setIssueLabelIDsMutation, vars, &out); err != nil {
		return err
	}
	if !out.IssueUpdate.Success {
		return fmt.Errorf("linear: set labels: request was not accepted")
	}
	return nil
}

// AddLabel idempotently ensures label is set on the Issue. It resolves (or
// creates) the team-scoped IssueLabel, and issues an update only when the
// Issue does not already carry it.
func (c *Client) AddLabel(ctx context.Context, id string, label string) error {
	internalID, err := c.resolveInternalID(ctx, id)
	if err != nil {
		return err
	}
	labelID, err := c.resolveLabelID(ctx, label)
	if err != nil {
		return err
	}
	current, err := c.issueLabelIDs(ctx, internalID)
	if err != nil {
		return fmt.Errorf("linear: add label %q to issue %s: %w", label, id, err)
	}
	for _, existing := range current {
		if existing == labelID {
			return nil
		}
	}
	if err := c.setIssueLabelIDs(ctx, internalID, append(current, labelID)); err != nil {
		return fmt.Errorf("linear: add label %q to issue %s: %w", label, id, err)
	}
	return nil
}

// RemoveLabel idempotently ensures label is not set on the Issue.
func (c *Client) RemoveLabel(ctx context.Context, id string, label string) error {
	internalID, err := c.resolveInternalID(ctx, id)
	if err != nil {
		return err
	}
	labelID, err := c.resolveLabelID(ctx, label)
	if err != nil {
		return err
	}
	current, err := c.issueLabelIDs(ctx, internalID)
	if err != nil {
		return fmt.Errorf("linear: remove label %q from issue %s: %w", label, id, err)
	}

	next := make([]string, 0, len(current))
	found := false
	for _, existing := range current {
		if existing == labelID {
			found = true
			continue
		}
		next = append(next, existing)
	}
	if !found {
		return nil
	}
	if err := c.setIssueLabelIDs(ctx, internalID, next); err != nil {
		return fmt.Errorf("linear: remove label %q from issue %s: %w", label, id, err)
	}
	return nil
}

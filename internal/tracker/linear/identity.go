package linear

import (
	"context"
	"fmt"
)

// resolveIdentifierQuery finds the internal id for a team-prefixed
// identifier (e.g. "FOR-345"). Linear's "issue" query field also accepts a
// bare identifier directly, but resolving it explicitly through a filtered
// search keeps this the one place that ever reads a raw identifier off the
// wire, so callers past this point deal only in the resolved internal id
// (see this ticket's "Issue identity" decision: the internal UUID is
// resolved internally and never surfaces).
const resolveIdentifierQuery = `
query($identifier: String!) {
  issues(filter: { identifier: { eq: $identifier } }) {
    nodes { id identifier }
  }
}`

const resolveTeamQuery = `
query($key: String!) {
  teams(filter: { key: { eq: $key } }) {
    nodes { id key }
  }
}`

// resolveInternalID resolves a team-prefixed identifier to Linear's internal
// UUID, for use in subsequent GraphQL calls that key on id.
func (c *Client) resolveInternalID(ctx context.Context, identifier string) (string, error) {
	var out struct {
		Issues struct {
			Nodes []struct {
				ID         string `json:"id"`
				Identifier string `json:"identifier"`
			} `json:"nodes"`
		} `json:"issues"`
	}
	vars := map[string]interface{}{"identifier": identifier}
	if err := c.graphQL(ctx, resolveIdentifierQuery, vars, &out); err != nil {
		return "", fmt.Errorf("linear: resolve identifier %s: %w", identifier, err)
	}
	if len(out.Issues.Nodes) == 0 {
		return "", &NotFoundError{ID: identifier}
	}
	return out.Issues.Nodes[0].ID, nil
}

// resolveTeamID resolves the configured team key to Linear's internal team
// UUID. Linear issue identifiers carry the team key, but mutations such as
// issueCreate and issueLabelCreate require the team UUID.
func (c *Client) resolveTeamID(ctx context.Context) (string, error) {
	if c.teamID != "" {
		return c.teamID, nil
	}
	var out struct {
		Teams struct {
			Nodes []struct {
				ID  string `json:"id"`
				Key string `json:"key"`
			} `json:"nodes"`
		} `json:"teams"`
	}
	vars := map[string]interface{}{"key": c.team}
	if err := c.graphQL(ctx, resolveTeamQuery, vars, &out); err != nil {
		return "", fmt.Errorf("linear: resolve team %s: %w", c.team, err)
	}
	if len(out.Teams.Nodes) == 0 {
		return "", &NotFoundError{ID: "team " + c.team}
	}
	c.teamID = out.Teams.Nodes[0].ID
	return c.teamID, nil
}

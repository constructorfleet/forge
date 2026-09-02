package linear

import (
	"context"
	"fmt"
	"time"

	"github.com/Teagan42/forge/internal/tracker"
)

// lnComment is the subset of Linear's Comment GraphQL shape Client
// normalizes. Unexported: this shape never leaves the linear package.
type lnComment struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	User      struct {
		Name string `json:"name"`
	} `json:"user"`
}

const getCommentsQuery = `
query($id: String!) {
  issue(id: $id) {
    comments {
      nodes { body createdAt user { name } }
    }
  }
}`

// GetComments fetches the comments on an Issue, normalized to
// tracker.Comment. Linear returns comments oldest first, matching the
// Tracker contract.
func (c *Client) GetComments(ctx context.Context, id string) ([]tracker.Comment, error) {
	internalID, err := c.resolveInternalID(ctx, id)
	if err != nil {
		return nil, err
	}

	var out struct {
		Issue struct {
			Comments struct {
				Nodes []lnComment `json:"nodes"`
			} `json:"comments"`
		} `json:"issue"`
	}
	if err := c.graphQL(ctx, getCommentsQuery, map[string]interface{}{"id": internalID}, &out); err != nil {
		return nil, fmt.Errorf("linear: get comments for issue %s: %w", id, err)
	}

	comments := make([]tracker.Comment, len(out.Issue.Comments.Nodes))
	for i, n := range out.Issue.Comments.Nodes {
		comments[i] = tracker.Comment{Author: n.User.Name, Body: n.Body, CreatedAt: n.CreatedAt}
	}
	return comments, nil
}

const commentCreateMutation = `
mutation($issueId: String!, $body: String!) {
  commentCreate(input: { issueId: $issueId, body: $body }) {
    success
    comment { body createdAt user { name } }
  }
}`

// AddComment posts a new comment on an Issue and returns it normalized from
// Linear's create-comment response — in particular the author identity and
// the server-assigned CreatedAt (see tracker.Tracker's AddComment doc
// comment).
func (c *Client) AddComment(ctx context.Context, id string, body string) (tracker.Comment, error) {
	internalID, err := c.resolveInternalID(ctx, id)
	if err != nil {
		return tracker.Comment{}, err
	}

	var out struct {
		CommentCreate struct {
			Success bool      `json:"success"`
			Comment lnComment `json:"comment"`
		} `json:"commentCreate"`
	}
	vars := map[string]interface{}{"issueId": internalID, "body": body}
	if err := c.graphQL(ctx, commentCreateMutation, vars, &out); err != nil {
		return tracker.Comment{}, fmt.Errorf("linear: add comment to issue %s: %w", id, err)
	}
	if !out.CommentCreate.Success {
		return tracker.Comment{}, fmt.Errorf("linear: add comment to issue %s: request was not accepted", id)
	}
	return tracker.Comment{
		Author:    out.CommentCreate.Comment.User.Name,
		Body:      out.CommentCreate.Comment.Body,
		CreatedAt: out.CommentCreate.Comment.CreatedAt,
	}, nil
}

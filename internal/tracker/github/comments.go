package github

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Teagan42/forge/internal/tracker"
)

// ghComment is the subset of GitHub's issue-comment JSON shape Client
// normalizes. Unexported: never leaves this package.
type ghComment struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

// GetComments fetches the comments on an Issue, oldest first (GitHub's
// native ordering), normalized to tracker.Comment.
func (c *Client) GetComments(ctx context.Context, id string) ([]tracker.Comment, error) {
	number, err := parseIssueID(id)
	if err != nil {
		return nil, err
	}

	var ghComments []ghComment
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", c.owner, c.repo, number)
	if err := c.do(ctx, http.MethodGet, path, nil, &ghComments); err != nil {
		return nil, err
	}

	comments := make([]tracker.Comment, len(ghComments))
	for i, gc := range ghComments {
		comments[i] = tracker.Comment{
			Author:    gc.User.Login,
			Body:      gc.Body,
			CreatedAt: gc.CreatedAt,
		}
	}
	return comments, nil
}

// AddComment posts a new comment on an Issue.
func (c *Client) AddComment(ctx context.Context, id string, body string) error {
	number, err := parseIssueID(id)
	if err != nil {
		return err
	}

	reqBody := struct {
		Body string `json:"body"`
	}{Body: body}

	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", c.owner, c.repo, number)
	return c.do(ctx, http.MethodPost, path, reqBody, nil)
}

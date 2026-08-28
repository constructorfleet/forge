package github

import (
	"context"
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

// GetComments fetches all comments on an Issue, oldest first (GitHub's
// native ordering), normalized to tracker.Comment. It requests the maximum
// page size and follows the Link "next" header across every page,
// accumulating results — otherwise only the first 30 (oldest) comments
// would be returned and the newest comments, which are most likely to
// carry current instructions or a needs-info signal (see CONTEXT.md
// "Needs-info resume flow"), would be silently dropped.
func (c *Client) GetComments(ctx context.Context, id string) ([]tracker.Comment, error) {
	number, err := parseIssueID(id)
	if err != nil {
		return nil, err
	}

	url := c.baseURL + c.issuePath(number, "/comments") + "?per_page=100"

	var comments []tracker.Comment
	for url != "" {
		var page []ghComment
		headers, err := c.doWithHeaders(ctx, http.MethodGet, url, nil, &page)
		if err != nil {
			return nil, err
		}
		for _, gc := range page {
			comments = append(comments, tracker.Comment{
				Author:    gc.User.Login,
				Body:      gc.Body,
				CreatedAt: gc.CreatedAt,
			})
		}
		url = nextPageURL(headers)
	}
	return comments, nil
}

// AddComment posts a new comment on an Issue and returns it normalized from
// GitHub's create-comment response — in particular the author login and
// server-assigned CreatedAt, which callers use as the authoritative
// identity/clock for a comment forge itself posted (see tracker.Tracker's
// AddComment doc comment).
func (c *Client) AddComment(ctx context.Context, id string, body string) (tracker.Comment, error) {
	number, err := parseIssueID(id)
	if err != nil {
		return tracker.Comment{}, err
	}

	reqBody := struct {
		Body string `json:"body"`
	}{Body: body}

	path := c.issuePath(number, "/comments")
	var resp ghComment
	if err := c.do(ctx, http.MethodPost, path, reqBody, &resp); err != nil {
		return tracker.Comment{}, err
	}
	return tracker.Comment{
		Author:    resp.User.Login,
		Body:      resp.Body,
		CreatedAt: resp.CreatedAt,
	}, nil
}

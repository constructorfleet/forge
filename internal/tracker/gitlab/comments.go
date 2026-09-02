package gitlab

import (
	"context"
	"net/http"
	"time"

	"github.com/Teagan42/forge/internal/tracker"
)

// glNote is the subset of GitLab's issue-note JSON shape Client normalizes.
// GitLab calls an issue comment a "note". Unexported: this shape never
// leaves the gitlab package.
//
// System is true for an activity record GitLab writes itself ("changed the
// description", "mentioned in merge request !12"). Those are not comments,
// so GetComments drops them.
type glNote struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	System    bool      `json:"system"`
	Author    struct {
		Username string `json:"username"`
	} `json:"author"`
}

// GetComments fetches the comments on an Issue, oldest first, normalized to
// tracker.Comment.
//
// GitLab sorts notes newest first by default, so the request asks for
// ascending order to meet the Tracker contract. It also asks for the maximum
// page size and follows the Link "next" header across every page. Without
// that, the newest comments — the ones most likely to carry current
// instructions or a needs-info answer — would be dropped.
func (c *Client) GetComments(ctx context.Context, id string) ([]tracker.Comment, error) {
	iid, err := parseIssueID(id)
	if err != nil {
		return nil, err
	}

	url := c.baseURL + c.issuePath(iid, "/notes") + "?per_page=100&order_by=created_at&sort=asc"

	var comments []tracker.Comment
	for url != "" {
		var page []glNote
		headers, err := c.doWithHeaders(ctx, http.MethodGet, url, nil, &page)
		if err != nil {
			return nil, err
		}
		for _, note := range page {
			if note.System {
				continue
			}
			comments = append(comments, tracker.Comment{
				Author:    note.Author.Username,
				Body:      note.Body,
				CreatedAt: note.CreatedAt,
			})
		}
		url = nextPageURL(headers)
	}
	return comments, nil
}

// AddComment posts a new note on an Issue and returns it normalized from
// GitLab's create-note response — in particular the author username and the
// server-assigned CreatedAt, which callers use as the authoritative identity
// and clock for a comment Forge posted itself (see tracker.Tracker's
// AddComment doc comment).
func (c *Client) AddComment(ctx context.Context, id string, body string) (tracker.Comment, error) {
	iid, err := parseIssueID(id)
	if err != nil {
		return tracker.Comment{}, err
	}

	reqBody := struct {
		Body string `json:"body"`
	}{Body: body}

	var resp glNote
	if err := c.do(ctx, http.MethodPost, c.issuePath(iid, "/notes"), reqBody, &resp); err != nil {
		return tracker.Comment{}, err
	}
	return tracker.Comment{
		Author:    resp.Author.Username,
		Body:      resp.Body,
		CreatedAt: resp.CreatedAt,
	}, nil
}

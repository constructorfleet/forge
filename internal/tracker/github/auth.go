package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
)

// ErrMissingToken indicates GITHUB_TOKEN is not set in the environment.
// VerifyAuth reports it without making any network request: a missing
// credential is knowable locally, and the whole point of a preflight is to
// fail before any side-effecting work (including a wasted round trip)
// begins.
var ErrMissingToken = errors.New("github: " + tokenEnvVar + " is not set; export it (e.g. GITHUB_TOKEN=$(gh auth token)) before running forge")

// VerifyAuth is a lightweight authenticated preflight: it confirms
// GITHUB_TOKEN is set and accepted for the configured owner/repo before a
// caller does any side-effecting work (creating a workspace, invoking an
// agent, transitioning an Issue). It implements tracker.AuthPreflighter.
//
// A missing token is reported immediately, with no request sent. Otherwise
// VerifyAuth issues a single GET against the repository itself, and the
// resulting error is one of the typed errors doWithHeaders already
// produces — *AuthenticationError for an invalid/rejected token (401),
// *AuthorizationError or *NotFoundError for a token that is accepted but
// cannot see this repository (403/404), or *tracker.RateLimitError — so
// callers can distinguish "unauthenticated" from "reachable but
// unauthorized" via errors.As.
func (c *Client) VerifyAuth(ctx context.Context) error {
	if os.Getenv(tokenEnvVar) == "" {
		return ErrMissingToken
	}
	path := fmt.Sprintf("/repos/%s/%s", c.owner, c.repo)
	if err := c.do(ctx, http.MethodGet, path, nil, nil); err != nil {
		return fmt.Errorf("github: auth preflight: %w", err)
	}
	return nil
}

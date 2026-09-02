package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
)

// ErrMissingToken indicates GITLAB_TOKEN is not set in the environment.
// VerifyAuth reports it without any network request: a missing credential is
// knowable locally, and the purpose of a preflight is to fail before any
// side-effecting work — including a wasted round trip — begins.
var ErrMissingToken = errors.New("gitlab: " + tokenEnvVar + " is not set; export it (a personal or project access token with the api scope) before running forge")

// VerifyAuth is a light authenticated preflight: it confirms GITLAB_TOKEN is
// set and accepted for the configured project before a caller does any
// side-effecting work (creating a workspace, invoking an agent, or
// transitioning an Issue). It implements tracker.AuthPreflighter.
//
// A missing token is reported at once, with no request sent. If a token is
// present, VerifyAuth sends one GET against the project itself. The
// resulting error is one of the typed errors doWithHeaders already produces
// — *AuthenticationError for a rejected token (401), *AuthorizationError or
// *NotFoundError for a token that GitLab accepts but that cannot see this
// project (403/404), or *tracker.RateLimitError — so a caller can tell
// "unauthenticated" from "reachable but unauthorized" with errors.As.
func (c *Client) VerifyAuth(ctx context.Context) error {
	if os.Getenv(tokenEnvVar) == "" {
		return ErrMissingToken
	}
	if err := c.do(ctx, http.MethodGet, c.projectPath(), nil, nil); err != nil {
		return fmt.Errorf("gitlab: auth preflight: %w", err)
	}
	return nil
}

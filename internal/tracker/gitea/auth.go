package gitea

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
)

// ErrMissingToken indicates GITEA_TOKEN is not set in the environment.
// VerifyAuth reports it without any network request: a missing credential is
// knowable locally, and the purpose of a preflight is to fail before any
// side-effecting work — including a wasted round trip — begins.
var ErrMissingToken = errors.New("gitea: " + tokenEnvVar + " is not set; export it (a Gitea access token) before running forge")

// VerifyAuth is a light authenticated preflight: it confirms GITEA_TOKEN is
// set and accepted for the configured repository before a caller does any
// side-effecting work (creating a workspace, invoking an agent, or
// transitioning an Issue). It implements tracker.AuthPreflighter.
//
// A missing token is reported at once, with no request sent. If a token is
// present, VerifyAuth sends one GET against the repository itself. The
// resulting error is one of the typed errors doWithHeaders already produces —
// *AuthenticationError for a rejected token (401), *AuthorizationError or
// *NotFoundError for a token that Gitea accepts but that cannot see this
// repository (403/404), or *tracker.RateLimitError — so a caller can tell
// "unauthenticated" from "reachable but unauthorized" with errors.As.
func (c *Client) VerifyAuth(ctx context.Context) error {
	if os.Getenv(tokenEnvVar) == "" {
		return ErrMissingToken
	}
	if err := c.do(ctx, http.MethodGet, c.repoPath(), nil, nil); err != nil {
		return fmt.Errorf("gitea: auth preflight: %w", err)
	}
	return nil
}

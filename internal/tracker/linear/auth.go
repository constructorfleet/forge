package linear

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// ErrMissingToken indicates LINEAR_API_KEY is not set in the environment.
// VerifyAuth reports it without any network request: a missing credential
// is knowable locally, and the purpose of a preflight is to fail before any
// side-effecting work — including a wasted round trip — begins.
var ErrMissingToken = errors.New("linear: " + tokenEnvVar + " is not set; export it (a Linear personal or workspace API key) before running forge")

// VerifyAuth is a light authenticated preflight: it confirms LINEAR_API_KEY
// is set and accepted before a caller does any side-effecting work (creating
// a workspace, invoking an agent, or transitioning an Issue). It implements
// tracker.AuthPreflighter.
//
// A missing key is reported at once, with no request sent. If a key is
// present, VerifyAuth sends Linear's "viewer" query, the cheapest
// authenticated call the API offers. A rejected key surfaces as
// *AuthenticationError, so a caller can tell "unauthenticated" apart from a
// reachability failure with errors.As.
func (c *Client) VerifyAuth(ctx context.Context) error {
	if os.Getenv(tokenEnvVar) == "" {
		return ErrMissingToken
	}
	var out struct {
		Viewer struct {
			ID string `json:"id"`
		} `json:"viewer"`
	}
	if err := c.graphQL(ctx, `query { viewer { id } }`, nil, &out); err != nil {
		return fmt.Errorf("linear: auth preflight: %w", err)
	}
	return nil
}

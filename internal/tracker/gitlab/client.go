// Package gitlab implements the tracker.Tracker and tracker.DependencyStore
// capabilities (see CONTEXT.md "Tracker Adapter") against the GitLab REST
// API. It uses only the standard library's net/http and encoding/json — no
// external GitLab SDK. All GitLab-specific JSON shapes are unexported and
// never leave this package; every exported method returns a domain or
// tracker type.
//
// The package implements the Tracker capability only. GitLab merge requests
// (the SCM capability) and GitLab pipelines (the CI capability) are not part
// of it.
package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/Teagan42/forge/internal/tracker"
)

// defaultBaseURL is the production GitLab REST API root.
const defaultBaseURL = "https://gitlab.com/api/v4"

// tokenEnvVar is the environment variable Client reads the GitLab token
// from at call time. The token is never stored in config or code (see the
// config package doc comment).
const tokenEnvVar = "GITLAB_TOKEN"

// Client is a GitLab REST API client scoped to a single project. It
// implements tracker.Tracker and tracker.DependencyStore.
type Client struct {
	httpClient *http.Client
	baseURL    string
	// project is the project path with namespace ("group/project") or the
	// numeric project ID. GitLab accepts both in the ":id" path position.
	project  string
	Provider string

	// DependencyOverrides configures the `.forge.yaml` Dependency Source
	// escape hatch (see CONTEXT.md "Dependency Source"). Keys and values
	// are Issue IDs; DependencyOverrides[issueID], if present, fully
	// replaces the Dependencies read from the tracker for that Issue. Nil
	// means no overrides are configured.
	DependencyOverrides map[string][]string

	// mu guards the native issue-link probe result below.
	mu sync.Mutex
	// linksProbed is true once the adapter has called the issue-link
	// endpoint at least once.
	linksProbed bool
	// linksAvailable records what that probe found: true if the instance
	// and the project tier expose typed issue links, false if they answer
	// 403 or 404. See dependencies.go and docs/adr/0027.
	linksAvailable bool
}

// NewClient builds a Client for the given project. httpClient is injected so
// tests can point it at an httptest.Server; a nil httpClient defaults to
// http.DefaultClient. An empty baseURL defaults to the production GitLab API
// root. project is the project path with namespace ("group/project") or the
// numeric project ID.
func NewClient(httpClient *http.Client, baseURL, project string) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		httpClient: httpClient,
		baseURL:    baseURL,
		project:    project,
		Provider:   "gitlab",
	}
}

var _ tracker.Tracker = (*Client)(nil)
var _ tracker.DependencyStore = (*Client)(nil)
var _ tracker.AuthPreflighter = (*Client)(nil)

// BaseURL returns the API root this Client sends requests to. It is
// exported for diagnostics: an operator who configures a self-managed
// instance must be able to confirm which host Forge targets.
func (c *Client) BaseURL() string { return c.baseURL }

// do issues an HTTP request against the GitLab API and decodes a JSON
// response body into out (if out is non-nil and the response has a body).
func (c *Client) do(ctx context.Context, method, path string, reqBody, out interface{}) error {
	_, err := c.doWithHeaders(ctx, method, c.baseURL+path, reqBody, out)
	return err
}

// doWithHeaders is like do but takes a fully-qualified URL (rather than a
// path relative to c.baseURL) and returns the response headers, so a caller
// that needs response metadata GitLab only exposes in headers — such as the
// Link header used for pagination — can read it.
//
// It maps each failing status onto a typed error. 401 becomes
// *AuthenticationError. 403 becomes *AuthorizationError. 404 becomes
// *NotFoundError. 400 and 422 become *ValidationError. 429 becomes
// *tracker.RateLimitError. A caller can then react to the class of failure
// without a dependency on a GitLab-specific error shape.
func (c *Client) doWithHeaders(ctx context.Context, method, fullURL string, reqBody, out interface{}) (http.Header, error) {
	var bodyReader io.Reader
	if reqBody != nil {
		encoded, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("gitlab: encode request body: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("gitlab: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// GitLab authenticates a personal, project, or group access token with
	// the PRIVATE-TOKEN header, not with an Authorization header.
	if token := os.Getenv(tokenEnvVar); token != "" {
		req.Header.Set("PRIVATE-TOKEN", token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitlab: request %s %s: %w", method, fullURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gitlab: read response %s %s: %w", method, fullURL, err)
	}

	if rlErr := rateLimitError(resp, respBody); rlErr != nil {
		return nil, rlErr
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, &AuthenticationError{Path: fullURL}
	case http.StatusForbidden:
		return nil, &AuthorizationError{Path: fullURL}
	case http.StatusNotFound:
		return nil, &NotFoundError{Path: fullURL}
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return nil, &ValidationError{Path: fullURL, Body: string(respBody)}
	}

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gitlab: %s %s: unexpected status %d: %s", method, fullURL, resp.StatusCode, string(respBody))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return nil, fmt.Errorf("gitlab: decode response %s %s: %w", method, fullURL, err)
		}
	}
	return resp.Header, nil
}

// rateLimitError classifies a 429 response as a rate-limit rejection and
// reads the reset time from the RateLimit-Reset header (unix seconds) or
// from Retry-After (seconds from now). It returns nil for every other
// response: GitLab reports a rate limit only with 429, so a plain 403 stays
// an authorization failure.
func rateLimitError(resp *http.Response, body []byte) error {
	if resp.StatusCode != http.StatusTooManyRequests {
		return nil
	}

	var resetAt time.Time
	if reset := resp.Header.Get("RateLimit-Reset"); reset != "" {
		if secs, err := strconv.ParseInt(reset, 10, 64); err == nil {
			resetAt = time.Unix(secs, 0)
		}
	}
	if resetAt.IsZero() {
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			if secs, err := strconv.ParseInt(retryAfter, 10, 64); err == nil {
				resetAt = time.Now().Add(time.Duration(secs) * time.Second)
			}
		}
	}

	var decoded struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &decoded)

	return &tracker.RateLimitError{ResetAt: resetAt, Message: decoded.Message}
}

// linkNextRe extracts the URL of the rel="next" entry from a GitLab
// pagination Link header, for example:
//
//	<https://gitlab.com/api/v4/...&page=2>; rel="next", <...>; rel="last"
var linkNextRe = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// nextPageURL returns the rel="next" URL from a response's Link header, or
// "" if there is no next page.
func nextPageURL(headers http.Header) string {
	m := linkNextRe.FindStringSubmatch(headers.Get("Link"))
	if m == nil {
		return ""
	}
	return m[1]
}

// providerID returns c.Provider, and defaults to "gitlab" when it is unset.
// It is the one source of the provider ID stamped onto every domain.Issue
// and tracker.DependencyEdge this Client produces.
func (c *Client) providerID() string {
	if c.Provider == "" {
		return "gitlab"
	}
	return c.Provider
}

// projectPath builds the "/projects/{project}" path prefix every endpoint
// shares. GitLab identifies a project by its URL-encoded path with
// namespace, so the "/" in "group/project" becomes "%2F" here.
func (c *Client) projectPath() string {
	return "/projects/" + url.PathEscape(c.project)
}

// issuePath builds the "/projects/{project}/issues/{iid}{suffix}" path every
// issue-scoped endpoint shares (the issue itself, its notes, its links), so
// that prefix is written once instead of at each call site.
func (c *Client) issuePath(iid int, suffix string) string {
	return fmt.Sprintf("%s/issues/%d%s", c.projectPath(), iid, suffix)
}

// NotFoundError is returned when the GitLab API answers 404. It is exported
// (unlike the raw GitLab JSON shapes) because a caller legitimately needs to
// tell "not found" apart from other failures — for example to treat a
// missing issue-link endpoint as "the tier does not expose links" rather
// than as an error.
type NotFoundError struct {
	Path string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("gitlab: not found: %s", e.Path)
}

// ValidationError is returned when the GitLab API answers 400 (Bad Request)
// or 422 (Unprocessable Entity). GitLab uses 400 where GitHub uses 422 for a
// malformed or incomplete request body, so both statuses map to this one
// type and a caller does not have to know which one GitLab chose.
type ValidationError struct {
	Path string
	Body string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("gitlab: validation failed: %s: %s", e.Path, e.Body)
}

// AuthenticationError is returned when the GitLab API answers 401
// (Unauthorized) — GitLab did not accept the credential the Client sent, or
// the Client sent none because GITLAB_TOKEN is unset. Exported so the
// startup auth preflight (see VerifyAuth) can report "unauthenticated"
// separately from "authenticated but forbidden" (AuthorizationError) and
// from "not found" (NotFoundError).
type AuthenticationError struct {
	Path string
}

func (e *AuthenticationError) Error() string {
	return fmt.Sprintf("gitlab: unauthenticated: %s: check that %s is set to a valid token", e.Path, tokenEnvVar)
}

// AuthorizationError is returned when the GitLab API answers 403
// (Forbidden) — the request reached GitLab and GitLab authenticated it, but
// the credential has no permission for the resource. GitLab also answers
// 403 for a feature the project tier does not include. Exported so
// VerifyAuth and the issue-link probe can tell this apart from
// AuthenticationError with errors.As.
type AuthorizationError struct {
	Path string
}

func (e *AuthorizationError) Error() string {
	return fmt.Sprintf("gitlab: forbidden: %s: the credential is authenticated but not authorized for this resource", e.Path)
}

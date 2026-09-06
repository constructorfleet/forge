// Package gitea implements the tracker.Tracker, tracker.DependencyStore,
// tracker.SCM, and tracker.CI capabilities (see CONTEXT.md "Tracker Adapter")
// against the Gitea REST API. Gitea (and Forgejo/Codeberg) expose an API that
// is close in shape to the GitHub API, so this package mirrors the github
// adapter's structure. It uses only the standard library's net/http and
// encoding/json. All Gitea-specific JSON shapes are unexported and never leave
// this package; every exported method returns a domain or tracker type.
package gitea

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/Teagan42/forge/internal/tracker"
)

// tokenEnvVar is the environment variable Client reads the Gitea token from at
// call time. The token never enters config or code (see the config package doc
// comment).
const tokenEnvVar = "GITEA_TOKEN"

// Client is a Gitea REST API client scoped to a single repository. It
// implements tracker.Tracker.
type Client struct {
	httpClient *http.Client
	// baseURL is the Gitea API root, for example
	// "https://gitea.example.com/api/v1". Gitea has no fixed host, so the
	// caller always supplies it (see cmd/forge's buildGiteaClient).
	baseURL  string
	owner    string
	repo     string
	Provider string

	// DependencyOverrides configures the `.forge.yaml` Dependency Source
	// escape hatch (see CONTEXT.md "Dependency Source"). Keys and values are
	// Issue IDs; DependencyOverrides[issueID], if present, fully replaces the
	// Dependencies parsed from that issue's body. Nil means no overrides are
	// configured.
	DependencyOverrides map[string][]string
}

// NewClient builds a Client for the given repository. httpClient is injected
// so tests can point it at an httptest.Server; a nil httpClient defaults to
// http.DefaultClient. baseURL is the Gitea API root ("<host>/api/v1").
func NewClient(httpClient *http.Client, baseURL, owner, repo string) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		httpClient: httpClient,
		baseURL:    baseURL,
		owner:      owner,
		repo:       repo,
		Provider:   "gitea",
	}
}

var _ tracker.Tracker = (*Client)(nil)
var _ tracker.DependencyStore = (*Client)(nil)
var _ tracker.AuthPreflighter = (*Client)(nil)
var _ tracker.SCM = (*Client)(nil)
var _ tracker.CI = (*Client)(nil)
var _ tracker.ReviewGetter = (*Client)(nil)
var _ tracker.ReviewsGetter = (*Client)(nil)
var _ tracker.MergeStatusGetter = (*Client)(nil)
var _ tracker.PullRequestTargetBranchGetter = (*Client)(nil)

// BaseURL returns the API root this Client sends requests to. It is exported
// for diagnostics: an operator who configures a self-managed instance must be
// able to confirm which host Forge targets.
func (c *Client) BaseURL() string { return c.baseURL }

// do issues an HTTP request against the Gitea API and decodes a JSON response
// body into out (if out is non-nil and the response has a body).
func (c *Client) do(ctx context.Context, method, path string, reqBody, out interface{}) error {
	_, err := c.doWithHeaders(ctx, method, c.baseURL+path, reqBody, out)
	return err
}

// doWithHeaders is like do but takes a fully-qualified URL (rather than a path
// relative to c.baseURL) and returns the response headers, so a caller that
// needs response metadata Gitea only exposes in headers — such as the Link
// header used for pagination — can read it.
//
// It maps each failing status onto a typed error. 401 becomes
// *AuthenticationError. 403 becomes *AuthorizationError. 404 becomes
// *NotFoundError. 409 becomes *ConflictError. 422 becomes *ValidationError. 429
// becomes *tracker.RateLimitError. A caller can then react to the class of
// failure without a dependency on a Gitea-specific error shape.
func (c *Client) doWithHeaders(ctx context.Context, method, fullURL string, reqBody, out interface{}) (http.Header, error) {
	var bodyReader io.Reader
	if reqBody != nil {
		encoded, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("gitea: encode request body: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("gitea: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Gitea authenticates a personal access token with the "token <value>"
	// Authorization scheme, not GitHub's "Bearer <value>".
	if token := os.Getenv(tokenEnvVar); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea: request %s %s: %w", method, fullURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gitea: read response %s %s: %w", method, fullURL, err)
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
	case http.StatusConflict:
		return nil, &ConflictError{Path: fullURL, Body: string(respBody)}
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return nil, &ValidationError{Path: fullURL, Body: string(respBody)}
	}

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gitea: %s %s: unexpected status %d: %s", method, fullURL, resp.StatusCode, string(respBody))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return nil, fmt.Errorf("gitea: decode response %s %s: %w", method, fullURL, err)
		}
	}
	return resp.Header, nil
}

// rateLimitError classifies a 429 response as a rate-limit rejection and reads
// the reset time from the X-RateLimit-Reset header (unix seconds) or from
// Retry-After (seconds from now). It returns nil for every other response:
// Gitea reports a rate limit only with 429, so a plain 403 stays an
// authorization failure.
func rateLimitError(resp *http.Response, body []byte) error {
	if resp.StatusCode != http.StatusTooManyRequests {
		return nil
	}

	var resetAt time.Time
	if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
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

// linkNextRe extracts the URL of the rel="next" entry from a Gitea pagination
// Link header, for example:
//
//	<https://gitea.example.com/api/v1/...&page=2>; rel="next", <...>; rel="last"
var linkNextRe = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// nextPageURL returns the rel="next" URL from a response's Link header, or ""
// if there is no next page.
func nextPageURL(headers http.Header) string {
	m := linkNextRe.FindStringSubmatch(headers.Get("Link"))
	if m == nil {
		return ""
	}
	return m[1]
}

// providerID returns c.Provider, and defaults to "gitea" when it is unset. It
// is the one source of the provider ID stamped onto every domain.Issue and
// tracker.DependencyEdge this Client produces.
func (c *Client) providerID() string {
	if c.Provider == "" {
		return "gitea"
	}
	return c.Provider
}

// repoPath builds the "/repos/{owner}/{repo}" path prefix every endpoint
// shares.
func (c *Client) repoPath() string {
	return fmt.Sprintf("/repos/%s/%s", c.owner, c.repo)
}

// issuePath builds the "/repos/{owner}/{repo}/issues/{index}{suffix}" path
// shared by every issue-scoped endpoint (the issue itself, comments, labels),
// so that prefix is written once instead of at each call site.
func (c *Client) issuePath(index int, suffix string) string {
	return fmt.Sprintf("%s/issues/%d%s", c.repoPath(), index, suffix)
}

// NotFoundError is returned when the Gitea API answers 404. It is exported
// (unlike the raw Gitea JSON shapes) because a caller legitimately needs to
// tell "not found" apart from other failures — for example to treat a missing
// branch-protection config as "no requirements" rather than an error.
type NotFoundError struct {
	Path string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("gitea: not found: %s", e.Path)
}

// ValidationError is returned when the Gitea API answers 400 (Bad Request) or
// 422 (Unprocessable Entity) for a malformed or incomplete request body.
// Exported so CreatePullRequest's idempotent-recovery path can tell it apart
// from other failures with errors.As.
type ValidationError struct {
	Path string
	Body string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("gitea: validation failed: %s: %s", e.Path, e.Body)
}

// ConflictError is returned when the Gitea API answers 409 (Conflict) — the
// request is valid, but the resource state does not permit it. Gitea answers
// 409 when a caller creates a pull request that already exists for the same
// head and base. Exported so CreatePullRequest can tell that state apart from a
// malformed request and recover the existing pull request instead of failing.
type ConflictError struct {
	Path string
	Body string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("gitea: conflict: %s: %s", e.Path, e.Body)
}

// AuthenticationError is returned when the Gitea API answers 401
// (Unauthorized) — Gitea did not accept the credential the Client sent, or the
// Client sent none because GITEA_TOKEN is unset. Exported so the startup auth
// preflight (see VerifyAuth) can report "unauthenticated" separately from
// "authenticated but forbidden" (AuthorizationError) and from "not found"
// (NotFoundError).
type AuthenticationError struct {
	Path string
}

func (e *AuthenticationError) Error() string {
	return fmt.Sprintf("gitea: unauthenticated: %s: check that %s is set to a valid token", e.Path, tokenEnvVar)
}

// AuthorizationError is returned when the Gitea API answers 403 (Forbidden) —
// the request reached Gitea and Gitea authenticated it, but the credential has
// no permission for the resource. Exported so VerifyAuth and other callers can
// tell this apart from AuthenticationError with errors.As.
type AuthorizationError struct {
	Path string
}

func (e *AuthorizationError) Error() string {
	return fmt.Sprintf("gitea: forbidden: %s: the credential is authenticated but not authorized for this resource", e.Path)
}

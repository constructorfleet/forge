// Package github implements the tracker.Tracker interface (see CONTEXT.md
// "Tracker Adapter") against the GitHub REST API using only the standard
// library's net/http and encoding/json — no external GitHub SDK. All
// GitHub-specific JSON shapes are unexported and never leak past this
// package's boundary; every exported method returns domain or tracker
// types.
package github

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

// defaultBaseURL is the production GitHub REST API root.
const defaultBaseURL = "https://api.github.com"

// tokenEnvVar is the environment variable Client reads the GitHub token
// from at call time. The token is never stored in config or code (see
// CONTEXT.md's config package doc and this ticket's constraints).
const tokenEnvVar = "GITHUB_TOKEN"

// Client is a github.com REST API client scoped to a single repository. It
// implements tracker.Tracker.
type Client struct {
	httpClient *http.Client
	baseURL    string
	owner      string
	repo       string

	// DependencyOverrides configures the `.forge.yaml` Dependency Source
	// escape hatch (see CONTEXT.md "Dependency Source"). Keys and values
	// are Issue IDs; DependencyOverrides[issueID], if present, fully
	// replaces the Dependencies parsed from that issue's body. Nil means
	// no overrides are configured.
	DependencyOverrides map[string][]string

	// Reachability checks whether a merge commit is reachable from an
	// applicable base branch in the local git checkout, the other half of
	// CheckExternal's satisfaction check (see external.go). Nil until the
	// caller wires a production implementation (cmd/forge); CheckExternal
	// errors rather than silently treating an unreachable check as
	// unsatisfied if it is unset.
	Reachability GitReachabilityChecker
}

// NewClient builds a Client for the given repository. httpClient is
// injected so tests can point it at an httptest.Server; a nil httpClient
// defaults to http.DefaultClient. An empty baseURL defaults to the
// production GitHub API root.
func NewClient(httpClient *http.Client, baseURL, owner, repo string) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		httpClient: httpClient,
		baseURL:    baseURL,
		owner:      owner,
		repo:       repo,
	}
}

var _ tracker.Tracker = (*Client)(nil)

// do issues an HTTP request against the GitHub API and decodes a JSON
// response body into out (if out is non-nil and the response has a body).
// It classifies rate-limit responses into *tracker.RateLimitError so
// callers can react without depending on GitHub-specific error shapes.
func (c *Client) do(ctx context.Context, method, path string, reqBody, out interface{}) error {
	_, err := c.doWithHeaders(ctx, method, c.baseURL+path, reqBody, out)
	return err
}

// doWithHeaders is like do but takes a fully-qualified URL (rather than a
// path relative to c.baseURL) and returns the response headers, so callers
// that need response metadata GitHub only exposes via headers — such as
// the Link header used for pagination — can inspect it.
func (c *Client) doWithHeaders(ctx context.Context, method, fullURL string, reqBody, out interface{}) (http.Header, error) {
	var bodyReader io.Reader
	if reqBody != nil {
		encoded, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("github: encode request body: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := os.Getenv(tokenEnvVar); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: request %s %s: %w", method, fullURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("github: read response %s %s: %w", method, fullURL, err)
	}

	if rlErr := rateLimitError(resp, respBody); rlErr != nil {
		return nil, rlErr
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, &NotFoundError{Path: fullURL}
	}

	if resp.StatusCode == http.StatusUnprocessableEntity {
		return nil, &ValidationError{Path: fullURL, Body: string(respBody)}
	}

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github: %s %s: unexpected status %d: %s", method, fullURL, resp.StatusCode, string(respBody))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return nil, fmt.Errorf("github: decode response %s %s: %w", method, fullURL, err)
		}
	}
	return resp.Header, nil
}

// rateLimitError classifies a response as a rate-limit rejection:
//   - a 403 with X-RateLimit-Remaining: 0 (primary limit exhausted)
//   - a 429 (secondary/abuse limit)
//   - a 403 with a Retry-After header (secondary/abuse limit — GitHub
//     often reports this with non-zero X-RateLimit-Remaining, so it must
//     be checked independently of the primary-limit case)
//
// It returns nil for any other response, including a plain 403 with none
// of the above signals.
func rateLimitError(resp *http.Response, body []byte) error {
	retryAfter := resp.Header.Get("Retry-After")
	primary := resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0"
	secondary := resp.StatusCode == http.StatusTooManyRequests
	secondaryRetryAfter := resp.StatusCode == http.StatusForbidden && retryAfter != ""
	if !primary && !secondary && !secondaryRetryAfter {
		return nil
	}

	var resetAt time.Time
	if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
		if secs, err := strconv.ParseInt(reset, 10, 64); err == nil {
			resetAt = time.Unix(secs, 0)
		}
	}
	if resetAt.IsZero() && retryAfter != "" {
		if secs, err := strconv.ParseInt(retryAfter, 10, 64); err == nil {
			resetAt = time.Now().Add(time.Duration(secs) * time.Second)
		}
	}

	var decoded struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &decoded)

	return &tracker.RateLimitError{ResetAt: resetAt, Message: decoded.Message}
}

// linkNextRe extracts the URL of the rel="next" entry from a GitHub
// pagination Link header, e.g.:
//
//	<https://api.github.com/...&page=2>; rel="next", <...>; rel="last"
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

// issuePath builds the "/repos/{owner}/{repo}/issues/{number}{suffix}" path
// shared by every issue-scoped endpoint (the issue itself, comments,
// labels), so that prefix is defined once instead of hand-built at each
// call site.
func (c *Client) issuePath(number int, suffix string) string {
	return fmt.Sprintf("/repos/%s/%s/issues/%d%s", c.owner, c.repo, number, suffix)
}

// NotFoundError is returned when the GitHub API responds 404. It is
// exported (unlike the raw GitHub JSON shapes) because callers legitimately
// need to distinguish "not found" from other failures — e.g. to treat a
// missing branch-protection config as "no requirements" rather than an
// error, or a missing label on RemoveLabel as already-idempotent.
type NotFoundError struct {
	Path string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("github: not found: %s", e.Path)
}

// ValidationError is returned when the GitHub API responds 422
// (Unprocessable Entity) — most relevantly for CreatePullRequest, which
// GitHub rejects this way when a pull request already exists for the given
// head/base pair. Exported so CreatePullRequest's idempotent-recovery path
// can distinguish it from other failures via errors.As.
type ValidationError struct {
	Path string
	Body string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("github: validation failed: %s: %s", e.Path, e.Body)
}

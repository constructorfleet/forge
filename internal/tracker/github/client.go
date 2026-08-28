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
	var bodyReader io.Reader
	if reqBody != nil {
		encoded, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("github: encode request body: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("github: build request: %w", err)
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
		return fmt.Errorf("github: request %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("github: read response %s %s: %w", method, path, err)
	}

	if rlErr := rateLimitError(resp, respBody); rlErr != nil {
		return rlErr
	}

	if resp.StatusCode == http.StatusNotFound {
		return &NotFoundError{Path: path}
	}

	if resp.StatusCode >= 300 {
		return fmt.Errorf("github: %s %s: unexpected status %d: %s", method, path, resp.StatusCode, string(respBody))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("github: decode response %s %s: %w", method, path, err)
		}
	}
	return nil
}

// rateLimitError classifies a response as a rate-limit rejection: a 403
// with X-RateLimit-Remaining: 0 (primary limit), or a 429 (secondary
// limit). It returns nil for any other response.
func rateLimitError(resp *http.Response, body []byte) error {
	primary := resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0"
	secondary := resp.StatusCode == http.StatusTooManyRequests
	if !primary && !secondary {
		return nil
	}

	var resetAt time.Time
	if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
		if secs, err := strconv.ParseInt(reset, 10, 64); err == nil {
			resetAt = time.Unix(secs, 0)
		}
	}

	var decoded struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &decoded)

	return &tracker.RateLimitError{ResetAt: resetAt, Message: decoded.Message}
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

// Package linear implements the tracker.Tracker and tracker.DependencyStore
// capabilities (see CONTEXT.md "Tracker Adapter") against Linear's GraphQL
// API. It uses only the standard library's net/http and encoding/json — no
// external Linear SDK. All Linear-specific GraphQL shapes are unexported
// and never leave this package; every exported method returns a domain or
// tracker type.
//
// Linear is a tracker-only provider: it exposes no source-control or CI
// capability, so this package implements Tracker and DependencyStore only
// (see CONTEXT.md and docs/adr for the tracker/scm/ci capability split).
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/Teagan42/forge/internal/tracker"
)

// defaultBaseURL is Linear's GraphQL API endpoint.
const defaultBaseURL = "https://api.linear.app/graphql"

// tokenEnvVar is the environment variable Client reads the Linear API key
// from at call time. The key is never stored in config or code (see the
// config package doc comment).
const tokenEnvVar = "LINEAR_API_KEY"

// Client is a Linear GraphQL API client scoped to a single team. It
// implements tracker.Tracker and tracker.DependencyStore.
type Client struct {
	httpClient *http.Client
	baseURL    string
	// team is the Linear team key (e.g. "FOR") that prefixes every issue
	// identifier this Client reads and writes.
	team     string
	teamID   string
	Provider string

	// DependencyOverrides configures the `.forge.yaml` Dependency Source
	// escape hatch (see CONTEXT.md "Dependency Source"). Keys and values
	// are Issue IDs; DependencyOverrides[issueID], if present, fully
	// replaces the Dependencies read from Linear's native relations for
	// that Issue. Nil means no overrides are configured.
	DependencyOverrides map[string][]string
}

// NewClient builds a Client scoped to team. httpClient is injected so tests
// can point it at an httptest.Server; a nil httpClient defaults to
// http.DefaultClient. An empty baseURL defaults to Linear's production
// GraphQL endpoint.
func NewClient(httpClient *http.Client, baseURL, team string) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		httpClient: httpClient,
		baseURL:    baseURL,
		team:       team,
		Provider:   "linear",
	}
}

var _ tracker.Tracker = (*Client)(nil)
var _ tracker.DependencyStore = (*Client)(nil)
var _ tracker.AuthPreflighter = (*Client)(nil)

// providerID returns c.Provider, defaulting to "linear" when unset — the
// single source of truth for the provider ID stamped onto domain.Issue and
// tracker.DependencyEdge values this Client produces.
func (c *Client) providerID() string {
	if c.Provider == "" {
		return "linear"
	}
	return c.Provider
}

// graphQL issues a POST against Linear's GraphQL endpoint with query and
// variables, decoding the response's "data" object into out. Linear reports
// GraphQL-level failures as a 200 carrying an "errors" array, so graphQL
// surfaces the first such error rather than silently decoding a null
// "data". A non-2xx HTTP status (for example 401 for an invalid or missing
// API key) maps to a typed error so a caller can react without depending on
// a Linear-specific error shape.
func (c *Client) graphQL(ctx context.Context, query string, variables map[string]interface{}, out interface{}) error {
	reqBody := struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables,omitempty"`
	}{Query: query, Variables: variables}

	encoded, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("linear: encode request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("linear: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// Linear authenticates a personal or workspace API key with a raw
	// Authorization header value — no "Bearer " prefix.
	if token := os.Getenv(tokenEnvVar); token != "" {
		req.Header.Set("Authorization", token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("linear: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("linear: read response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return &AuthenticationError{}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return &tracker.RateLimitError{}
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("linear: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &envelope); err != nil {
			return fmt.Errorf("linear: decode response: %w", err)
		}
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("linear: graphql: %s", envelope.Errors[0].Message)
	}
	if out != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return fmt.Errorf("linear: graphql: decode data: %w", err)
		}
	}
	return nil
}

// AuthenticationError is returned when Linear's API answers 401 — the API
// key is missing or invalid. Exported so the startup auth preflight (see
// VerifyAuth) can report "unauthenticated" distinctly with errors.As.
type AuthenticationError struct{}

func (e *AuthenticationError) Error() string {
	return fmt.Sprintf("linear: unauthenticated: check that %s is set to a valid API key", tokenEnvVar)
}

// NotFoundError is returned when a query resolves no matching Issue.
// Exported so a caller can tell "not found" apart from other failures.
type NotFoundError struct {
	ID string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("linear: issue not found: %s", e.ID)
}

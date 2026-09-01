package httpworker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/execution/remote"
)

// Client drives a Server over HTTP+JSON, implementing remote.WorkerClient —
// the one concrete transport behind the seam (issue #345). Every method's
// only failure mode visible to the Remote backend is an ordinary error: a
// non-2xx response or a transport fault (connection refused, timeout) both
// surface the same way a fake worker's programmed error does, so
// remote.RecoverFunc classifies them identically regardless of transport.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient returns a Client that talks to the worker daemon at baseURL
// (e.g. "http://worker.example.com:9090"). A nil httpClient uses
// http.DefaultClient.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

// Ping checks that the worker daemon is reachable and healthy, for
// preflight (constructorfleet/forge#343's wiring calls this before ever
// dispatching work).
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+pathHealth, nil)
	if err != nil {
		return fmt.Errorf("httpworker: build health request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("httpworker: worker unreachable at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("httpworker: worker at %s reported unhealthy status %d", c.baseURL, resp.StatusCode)
	}
	return nil
}

// PrepareWorkspace implements remote.WorkerClient.
func (c *Client) PrepareWorkspace(ctx context.Context, req execution.WorkspaceRequest) (remote.WorkerHandle, domain.Workspace, error) {
	var resp prepareResponse
	if err := c.call(ctx, pathPrepare, req, &resp); err != nil {
		return "", domain.Workspace{}, err
	}
	return remote.WorkerHandle(resp.Handle), resp.Workspace, nil
}

// Execute implements remote.WorkerClient.
func (c *Client) Execute(ctx context.Context, handle remote.WorkerHandle, cmd execution.Command) (execution.Result, error) {
	var resp executeResponse
	if err := c.call(ctx, pathExecute, executeRequest{Handle: string(handle), Command: cmd}, &resp); err != nil {
		return execution.Result{}, err
	}
	return resp.Result, nil
}

// RunAgent implements remote.WorkerClient.
func (c *Client) RunAgent(ctx context.Context, handle remote.WorkerHandle, req agent.AgentRequest) (agent.AgentResult, error) {
	var resp agentResponse
	if err := c.call(ctx, pathAgent, agentRequest{Handle: string(handle), Request: req}, &resp); err != nil {
		return agent.AgentResult{}, err
	}
	return resp.Result, nil
}

// Heartbeat implements remote.WorkerClient.
func (c *Client) Heartbeat(ctx context.Context, handle remote.WorkerHandle) error {
	return c.call(ctx, pathHeartbeat, handleRequest{Handle: string(handle)}, nil)
}

// FetchResult implements remote.WorkerClient.
func (c *Client) FetchResult(ctx context.Context, handle remote.WorkerHandle) (remote.WorkerResult, error) {
	var resp resultResponse
	if err := c.call(ctx, pathResult, handleRequest{Handle: string(handle)}, &resp); err != nil {
		return remote.WorkerResult{}, err
	}
	return remote.WorkerResult{Bundle: resp.Bundle, HeadSHA: resp.HeadSHA}, nil
}

// Cleanup implements remote.WorkerClient.
func (c *Client) Cleanup(ctx context.Context, handle remote.WorkerHandle) error {
	return c.call(ctx, pathCleanup, handleRequest{Handle: string(handle)}, nil)
}

// call POSTs body as JSON to path and decodes a 2xx JSON response into out
// (skipped when out is nil). A non-2xx response's errorResponse body
// becomes the returned error's message; a request that never reaches the
// worker (dial failure, timeout, context cancellation) returns the
// underlying transport error unwrapped-visible via errors.Is/As, so a
// caller-supplied RecoverFunc can inspect it exactly as it would a fake
// worker's programmed error.
func (c *Client) call(ctx context.Context, path string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("httpworker: encode request for %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("httpworker: build request for %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("httpworker: %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		var errResp errorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("httpworker: %s: %s", path, errResp.Error)
		}
		return fmt.Errorf("httpworker: %s: status %d", path, resp.StatusCode)
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("httpworker: decode response for %s: %w", path, err)
	}
	return nil
}

var _ remote.WorkerClient = (*Client)(nil)

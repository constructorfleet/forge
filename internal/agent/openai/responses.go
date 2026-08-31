package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/agent/clicommon"
)

var _ agent.Agent = (*ResponsesAdapter)(nil)

// defaultResponsesBaseURL is the production OpenAI Responses API root.
const defaultResponsesBaseURL = "https://api.openai.com/v1"

// defaultModel is the model used when Model is empty.
const defaultModel = "gpt-4o"

// ResponsesAdapter is a production Agent Adapter that invokes an
// OpenAI-compatible Responses API endpoint over HTTP.
type ResponsesAdapter struct {
	// HTTPClient issues the request. Defaults to http.DefaultClient when
	// nil; tests inject a client pointed at an httptest.Server so they
	// never hit the real network.
	HTTPClient httpDoer

	// BaseURL is the API root, without a trailing slash. Defaults to
	// defaultResponsesBaseURL ("https://api.openai.com/v1"); overriding it
	// is how this Adapter targets any OpenAI-compatible Responses endpoint.
	BaseURL string

	// Model is the model name sent in every request. Defaults to
	// defaultModel ("gpt-4o") when empty.
	Model string

	// APIKeyEnvVar names the environment variable this Adapter reads its
	// bearer credential from at call time. Defaults to defaultAPIKeyEnvVar
	// ("OPENAI_API_KEY") when empty.
	APIKeyEnvVar string
}

// responsesRequest is the minimal Responses API request body this Adapter
// sends: a model name and the fully-rendered prompt as a single string
// input.
type responsesRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// responsesResponseBody is the subset of the Responses API's response shape
// this Adapter reads: the output array's text content, and (when present)
// token usage.
type responsesResponseBody struct {
	Output []struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// text concatenates every output_text content item across resp's output,
// reconstructing the equivalent of the Responses API SDK's convenience
// output_text field from the raw REST shape.
func (resp responsesResponseBody) text() string {
	var b strings.Builder
	for _, item := range resp.Output {
		for _, c := range item.Content {
			if c.Type != "output_text" {
				continue
			}
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// Execute implements agent.Agent. It builds a prompt from req, POSTs it to
// the configured Responses API endpoint, and resolves the response into a
// structured agent.AgentResult. Ordinary failures (transport errors,
// non-2xx statuses, no structured result) surface through AgentResult.Status
// rather than a returned error, matching every other Agent Adapter; context
// cancellation is the one exception, additionally surfaced as a wrapped
// ctx.Err().
func (a *ResponsesAdapter) Execute(ctx context.Context, req agent.AgentRequest) (res agent.AgentResult, err error) {
	prompt := clicommon.BuildPrompt("openai-responses", req)

	// Never leave a failed run with a blank transcript (issue #257): if
	// Execute returns FAILED, persist the diagnostic Summary as a fallback
	// event. The success path emits the response text below, and a FAILED
	// result carries no response text, so this does not double-emit.
	defer func() {
		if res.Status == agent.StatusFailed {
			emitTranscript(req, res.Summary)
		}
	}()

	client := a.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	baseURL := a.BaseURL
	if baseURL == "" {
		baseURL = defaultResponsesBaseURL
	}
	model := a.Model
	if model == "" {
		model = defaultModel
	}

	reqBody := responsesRequest{Model: model, Input: prompt}
	status, body, err := postJSON(ctx, client, baseURL+"/responses", a.APIKeyEnvVar, reqBody)

	if ctxErr := ctx.Err(); ctxErr != nil {
		return agent.AgentResult{
			Status:  agent.StatusFailed,
			Summary: clicommon.DiagnosticSummary(fmt.Sprintf("openai-responses adapter: cancelled: %v", ctxErr), string(body), ""),
		}, fmt.Errorf("openai-responses adapter: cancelled: %w", ctxErr)
	}
	if err != nil {
		return failedResult(fmt.Sprintf("openai-responses adapter: request error: %v", err), ""), nil
	}
	if status < 200 || status >= 300 {
		return failedResult(fmt.Sprintf("openai-responses adapter: unexpected status %d", status), string(body)), nil
	}

	var resp responsesResponseBody
	if err := json.Unmarshal(body, &resp); err != nil {
		return failedResult(fmt.Sprintf("openai-responses adapter: decode response: %v", err), string(body)), nil
	}

	text := resp.text()
	emitTranscript(req, text)

	var usage *agent.TokenUsage
	if resp.Usage != nil {
		usage = &agent.TokenUsage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens}
	}

	return buildResult("openai-responses", text, usage), nil
}

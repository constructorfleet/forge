package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/agent/clicommon"
)

var _ agent.Agent = (*ChatCompletionsAdapter)(nil)

// defaultChatCompletionsBaseURL is the production OpenAI Chat Completions
// API root.
const defaultChatCompletionsBaseURL = "https://api.openai.com/v1"

// ChatCompletionsAdapter is a production Agent Adapter that invokes an
// OpenAI-compatible Chat Completions API endpoint over HTTP.
type ChatCompletionsAdapter struct {
	// HTTPClient issues the request. Defaults to http.DefaultClient when
	// nil; tests inject a client pointed at an httptest.Server so they
	// never hit the real network.
	HTTPClient httpDoer

	// BaseURL is the API root, without a trailing slash. Defaults to
	// defaultChatCompletionsBaseURL ("https://api.openai.com/v1");
	// overriding it is how this Adapter targets any OpenAI-compatible Chat
	// Completions endpoint (e.g. a self-hosted or third-party server).
	BaseURL string

	// Model is the model name sent in every request. Defaults to
	// defaultModel ("gpt-4o") when empty.
	Model string

	// APIKeyEnvVar names the environment variable this Adapter reads its
	// bearer credential from at call time. Defaults to defaultAPIKeyEnvVar
	// ("OPENAI_API_KEY") when empty.
	APIKeyEnvVar string
}

// chatMessage is one entry of a Chat Completions request's messages array.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatCompletionsRequest is the minimal Chat Completions API request body
// this Adapter sends: a model name and the fully-rendered prompt as a
// single user message.
type chatCompletionsRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

// chatCompletionsResponseBody is the subset of the Chat Completions API's
// response shape this Adapter reads: the first choice's message content,
// and (when present) token usage.
type chatCompletionsResponseBody struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// text returns the first choice's message content, or "" if resp carries no
// choices.
func (resp chatCompletionsResponseBody) text() string {
	if len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Message.Content
}

// Execute implements agent.Agent. It builds a prompt from req, POSTs it to
// the configured Chat Completions API endpoint, and resolves the response
// into a structured agent.AgentResult. Ordinary failures (transport errors,
// non-2xx statuses, no structured result) surface through AgentResult.Status
// rather than a returned error, matching every other Agent Adapter; context
// cancellation is the one exception, additionally surfaced as a wrapped
// ctx.Err().
func (a *ChatCompletionsAdapter) Execute(ctx context.Context, req agent.AgentRequest) (res agent.AgentResult, err error) {
	prompt := clicommon.BuildPrompt("openai-chat-completions", req)

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
		baseURL = defaultChatCompletionsBaseURL
	}
	model := a.Model
	if model == "" {
		model = defaultModel
	}

	reqBody := chatCompletionsRequest{Model: model, Messages: []chatMessage{{Role: "user", Content: prompt}}}
	status, body, err := postJSON(ctx, client, baseURL+"/chat/completions", a.APIKeyEnvVar, reqBody)

	if ctxErr := ctx.Err(); ctxErr != nil {
		return agent.AgentResult{
			Status:  agent.StatusFailed,
			Summary: clicommon.DiagnosticSummary(fmt.Sprintf("openai-chat-completions adapter: cancelled: %v", ctxErr), string(body), ""),
		}, fmt.Errorf("openai-chat-completions adapter: cancelled: %w", ctxErr)
	}
	if err != nil {
		return failedResult(fmt.Sprintf("openai-chat-completions adapter: request error: %v", err), ""), nil
	}
	if status < 200 || status >= 300 {
		return failedResult(fmt.Sprintf("openai-chat-completions adapter: unexpected status %d", status), string(body)), nil
	}

	var resp chatCompletionsResponseBody
	if err := json.Unmarshal(body, &resp); err != nil {
		return failedResult(fmt.Sprintf("openai-chat-completions adapter: decode response: %v", err), string(body)), nil
	}

	text := resp.text()
	emitTranscript(req, text)

	var usage *agent.TokenUsage
	if resp.Usage != nil {
		usage = &agent.TokenUsage{InputTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens}
	}

	// ModeReview/ModeStructured return the model's message verbatim as
	// Summary via the shared clicommon.ModeResult, instead of the
	// {status, summary} parse buildResult applies for ModeImplement.
	if modeRes, handled := clicommon.ModeResult("openai-chat-completions", req.Mode, text, string(body), "", status); handled {
		modeRes.Usage = usage
		return modeRes, nil
	}

	return buildResult("openai-chat-completions", text, usage), nil
}

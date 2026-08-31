// Package clicommon holds logic shared by Forge's Agent Adapters (see
// CONTEXT.md "Agent Adapter"): the result-envelope contract every backend is
// instructed to emit, the prompt built from an agent.AgentRequest, and the
// small diagnostic/env-sanitization helpers each adapter needs. Individual
// adapters (internal/agent/codex, internal/agent/opencode, internal/agent/pi,
// internal/agent/openai) own their own invocation mechanics (subprocess vs.
// HTTP) and delegate the backend-agnostic parts here, mirroring
// internal/agent/claude without duplicating it four times over.
package clicommon

import (
	"encoding/json"
	"regexp"

	"github.com/Teagan42/forge/internal/agent"
)

// fencedJSONBlock matches fenced code blocks (optionally tagged "json") so
// ParseStructuredResult can pull the outcome a backend was instructed to
// emit (see ResultContract) out of otherwise free-form output. The closing
// fence is anchored to the start of a line ((?m:^```)) so a result whose
// summary contains a literal ``` sequence mid-line doesn't get mistaken for
// the block's end.
var fencedJSONBlock = regexp.MustCompile("(?s)```(?:json)?[ \t]*\r?\n(.*?)\r?\n(?m:^```)")

// trailingComma matches a comma followed only by whitespace before a
// closing `}` or `]`, e.g. `"summary": "…",}`. LLMs occasionally emit this
// cosmetic malformation; stripping it lets an otherwise well-formed result
// envelope parse instead of being discarded as FAILED.
var trailingComma = regexp.MustCompile(`,(\s*[}\]])`)

// repairTrailingCommas removes trailing commas before closing braces or
// brackets, at any nesting depth, so a single small malformation doesn't
// sink an otherwise-valid JSON payload.
func repairTrailingCommas(raw string) string {
	return trailingComma.ReplaceAllString(raw, "$1")
}

// StructuredResult is the JSON shape every Agent Adapter instructs its
// backend to emit as the last fenced code block in its output (see
// ResultContract).
type StructuredResult struct {
	Status    string           `json:"status"`
	Summary   string           `json:"summary"`
	NeedsInfo *NeedsInfoFields `json:"needs_info,omitempty"`
	FollowUps []FollowUpFields `json:"follow_ups,omitempty"`
	Usage     *UsageFields     `json:"usage,omitempty"`
}

// NeedsInfoFields carries the question/context a NEEDS_INFO result requires.
type NeedsInfoFields struct {
	Question string `json:"question"`
	Context  string `json:"context"`
}

// FollowUpFields carries one out-of-scope observation the backend wants
// filed as a new tracker Issue (see agent.FollowUpReport). Independent of
// status: a backend may emit these alongside any outcome.
type FollowUpFields struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// UsageFields carries a backend's optional token accounting for one
// invocation.
type UsageFields struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ParseStructuredResult scans text for fenced code blocks and returns the
// last one that parses as a valid StructuredResult with a recognized
// status. A backend may emit other fenced blocks (code, examples) earlier
// in its output; only the final well-formed status block is authoritative,
// matching ResultContract's instruction to emit it last.
func ParseStructuredResult(text string) (StructuredResult, bool) {
	matches := fencedJSONBlock.FindAllStringSubmatch(text, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		block := matches[i][1]
		res, ok := unmarshalStructuredResult(block)
		if !ok {
			// Retry with known cosmetic malformations repaired (e.g. a
			// trailing comma before the closing brace).
			res, ok = unmarshalStructuredResult(repairTrailingCommas(block))
		}
		if !ok {
			continue
		}
		switch agent.AgentStatus(res.Status) {
		case agent.StatusImplemented, agent.StatusNeedsInfo, agent.StatusFailed:
			return res, true
		}
	}
	return StructuredResult{}, false
}

func unmarshalStructuredResult(raw string) (StructuredResult, bool) {
	var res StructuredResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return StructuredResult{}, false
	}
	return res, true
}

// ToTokenUsage converts a StructuredResult's optional Usage into an
// agent.TokenUsage, or nil when the backend didn't report usage.
func ToTokenUsage(in *UsageFields) *agent.TokenUsage {
	if in == nil {
		return nil
	}
	return &agent.TokenUsage{
		InputTokens:  in.InputTokens,
		OutputTokens: in.OutputTokens,
	}
}

// ToFollowUps converts a StructuredResult's optional FollowUps into
// agent.FollowUpReports, or nil when the backend reported none.
func ToFollowUps(in []FollowUpFields) []agent.FollowUpReport {
	if len(in) == 0 {
		return nil
	}
	out := make([]agent.FollowUpReport, len(in))
	for i, f := range in {
		out[i] = agent.FollowUpReport{Title: f.Title, Body: f.Body}
	}
	return out
}

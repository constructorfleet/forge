package claude

import (
	"encoding/json"
	"regexp"

	"github.com/Teagan42/forge/internal/agent"
)

// fencedJSONBlock matches fenced code blocks (optionally tagged "json") so
// parseStructuredResult can pull the outcome Claude Code was instructed to
// emit (see resultContract in prompt.go) out of otherwise free-form output.
// The closing fence is anchored to the start of a line ((?m:^```)) so a
// result whose summary contains a literal ``` sequence mid-line doesn't get
// mistaken for the block's end, truncating (and discarding) the rest of
// the JSON.
var fencedJSONBlock = regexp.MustCompile("(?s)```(?:json)?[ \t]*\r?\n(.*?)\r?\n(?m:^```)")

// trailingComma matches a comma followed only by whitespace before a
// closing `}` or `]`, e.g. `"summary": "…",}`. LLMs occasionally emit
// this cosmetic malformation; stripping it lets an otherwise well-formed
// result envelope parse instead of being discarded as FAILED.
var trailingComma = regexp.MustCompile(`,(\s*[}\]])`)

// repairTrailingCommas removes trailing commas before closing braces or
// brackets, at any nesting depth, so a single small malformation doesn't
// sink an otherwise-valid JSON payload.
func repairTrailingCommas(raw string) string {
	return trailingComma.ReplaceAllString(raw, "$1")
}

// structuredResult is the JSON shape Claude Code is instructed to emit as
// the last fenced code block in its output (see resultContract).
type structuredResult struct {
	Status    string           `json:"status"`
	Summary   string           `json:"summary"`
	NeedsInfo *needsInfoFields `json:"needs_info,omitempty"`
	Usage     *usageFields     `json:"usage,omitempty"`
}

type needsInfoFields struct {
	Question string `json:"question"`
	Context  string `json:"context"`
}

type usageFields struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// parseStructuredResult scans stdout for fenced code blocks and returns the
// last one that parses as a valid structuredResult with a recognized
// status. Claude Code may emit other fenced blocks (code, examples) earlier
// in its output; only the final well-formed status block is authoritative,
// matching resultContract's instruction to emit it last.
func parseStructuredResult(stdout string) (structuredResult, bool) {
	matches := fencedJSONBlock.FindAllStringSubmatch(stdout, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		block := matches[i][1]
		res, ok := unmarshalStructuredResult(block)
		if !ok {
			// Retry with known cosmetic malformations repaired (e.g. a
			// trailing comma before the closing brace). This is a bounded
			// fallback: if the repaired text still doesn't parse as a
			// recognized result, the block is skipped just like before.
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
	return structuredResult{}, false
}

func unmarshalStructuredResult(raw string) (structuredResult, bool) {
	var res structuredResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return structuredResult{}, false
	}
	return res, true
}

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
		var res structuredResult
		if err := json.Unmarshal([]byte(matches[i][1]), &res); err != nil {
			continue
		}
		switch agent.AgentStatus(res.Status) {
		case agent.StatusImplemented, agent.StatusNeedsInfo, agent.StatusFailed:
			return res, true
		}
	}
	return structuredResult{}, false
}

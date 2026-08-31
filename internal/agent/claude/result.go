package claude

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/Teagan42/forge/internal/agent"
)

// resultJSONSchema is the JSON Schema for structuredResult, passed to the
// Claude Code CLI via `--json-schema` (issue 20/ticket 32) so the CLI
// itself enforces the {status, summary, needs_info, usage} envelope shape
// on the model's final answer, instead of Forge inferring it from a fenced
// ```json block after the fact. It is derived from structuredResult (issue
// 194) via buildResultJSONSchema, rather than hand-maintained, so it cannot
// drift from the struct that decodes the schema-constrained output.
var resultJSONSchema = func() string {
	schema, err := buildResultJSONSchema()
	if err != nil {
		panic(fmt.Sprintf("claude: derive result JSON schema: %v", err))
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("claude: marshal result JSON schema: %v", err))
	}
	return string(encoded)
}()

// buildResultJSONSchema derives a JSON Schema for structuredResult using
// jsonschema-go's struct-based inference, then applies the one refinement
// inference cannot express: restricting "status" to the recognized
// agent.AgentStatus values. Inference alone already produces the rest of
// the shape the hand-authored schema enforced — required:[status,summary],
// additionalProperties:false (jsonschema-go's default for structs), and the
// nested needs_info/usage objects — because structuredResult's optional
// fields are marked "omitempty".
//
// One intentional relaxation from the old hand-authored schema: pointer
// fields (NeedsInfo, Usage) also accept an explicit JSON null in addition
// to being omitted, since jsonschema-go treats a Go pointer as nullable.
// The Claude Code CLI never emits null for these fields, so this is not
// reachable in practice.
func buildResultJSONSchema() (*jsonschema.Schema, error) {
	schema, err := jsonschema.For[structuredResult](nil)
	if err != nil {
		return nil, fmt.Errorf("infer schema for structuredResult: %w", err)
	}
	status, ok := schema.Properties["status"]
	if !ok {
		return nil, fmt.Errorf("inferred schema missing %q property", "status")
	}
	status.Enum = []any{
		string(agent.StatusImplemented),
		string(agent.StatusNeedsInfo),
		string(agent.StatusFailed),
	}
	return schema, nil
}

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
	FollowUps []followUpFields `json:"follow_ups,omitempty"`
	Usage     *usageFields     `json:"usage,omitempty"`
}

type needsInfoFields struct {
	Question string `json:"question"`
	Context  string `json:"context,omitempty"`
}

// followUpFields carries one out-of-scope observation Claude Code wants
// filed as a new tracker Issue (see agent.FollowUpReport / automatic self
// reporting).
type followUpFields struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

type usageFields struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

// parseStructuredResult scans stdout for fenced code blocks and returns the
// last one that parses as a valid structuredResult with a recognized
// status. Claude Code may emit other fenced blocks (code, examples) earlier
// in its output; only the final well-formed status block is authoritative,
// matching resultContract's instruction to emit it last.
//
// Since issue 20/ticket 32, Execute tries parseSchemaResult first: this
// tolerant, fenced-block-scanning parser (originally ticket 27's) is now
// only a fallback for output that didn't go through `--json-schema`
// enforcement (e.g. an older CLI, or a text-mode path).
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

// parseSchemaResult decodes raw directly as a structuredResult, with none
// of parseStructuredResult's fenced-block extraction or trailing-comma
// repair: when the CLI was invoked with `--json-schema` (resultJSONSchema),
// it already guarantees the model's final answer is a single JSON object
// conforming to that schema, so this path only needs to distinguish
// well-formed output from empty/corrupt output — it is not a tolerant
// parser. ok is false for empty input, invalid JSON, or an unrecognized
// status, in which case Execute falls back to parseStructuredResult for
// compatibility with any text-mode/non-schema path.
func parseSchemaResult(raw string) (structuredResult, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return structuredResult{}, false
	}
	res, ok := unmarshalStructuredResult(raw)
	if !ok {
		return structuredResult{}, false
	}
	switch agent.AgentStatus(res.Status) {
	case agent.StatusImplemented, agent.StatusNeedsInfo, agent.StatusFailed:
		return res, true
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

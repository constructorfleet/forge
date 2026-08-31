package agentreviewer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// axisFinding is one finding as emitted by the axis agent's JSON findings
// envelope (see rubric.md's "Output contract" section for the exact shape
// the agent is instructed to produce).
type axisFinding struct {
	Severity   string  `json:"severity"`
	Confidence float64 `json:"confidence"`
	File       string  `json:"file"`
	Line       int     `json:"line"`
	Message    string  `json:"message"`
	Evidence   string  `json:"evidence"`
	Remedy     string  `json:"remedy"`
}

// envelope is the review-shaped JSON contract one axis's agent invocation
// emits, per rubric.md. AgentResult carries no dedicated findings field (its
// Summary is the only text surface a real backend exposes), so this is
// parsed out of AgentResult.Summary.
type envelope struct {
	Axis     string        `json:"axis"`
	Findings []axisFinding `json:"findings"`

	// Assurances is a list of things this axis explicitly checked and
	// found clean/correct (issue #176), as opposed to Findings (defects).
	// It never affects the verdict — synthesizeFindings only uses it to
	// detect assurance-vs-finding tensions against a DIFFERENT axis's
	// findings (see assuranceFindingTensions in synthesizer.go).
	Assurances []string `json:"assurances"`
}

// parseEnvelope extracts and decodes the JSON findings envelope from raw,
// the axis agent's raw output text. raw is expected to be (or to contain,
// possibly wrapped in a markdown code fence or surrounded by incidental
// prose) exactly one JSON object matching envelope's shape. An empty raw, or
// text containing no parseable JSON object, is reported as an error —
// tolerant degradation for a malformed envelope is a later ticket (#161).
func parseEnvelope(raw string) (envelope, error) {
	text := extractJSONObject(raw)
	if text == "" {
		return envelope{}, fmt.Errorf("agentreviewer: no JSON findings envelope found in agent output")
	}

	var env envelope
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		return envelope{}, fmt.Errorf("agentreviewer: parse findings envelope: %w", err)
	}
	return env, nil
}

// extractJSONObject returns the outermost {...} JSON object found in raw,
// tolerating a surrounding markdown code fence (` ```json ... ``` ` or
// ` ``` ... ``` `) and incidental leading/trailing prose the agent may emit
// despite being instructed not to. Returns "" if raw contains no plausible
// JSON object.
func extractJSONObject(raw string) string {
	text := strings.TrimSpace(raw)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```JSON")
		text = strings.TrimPrefix(text, "```")
		if idx := strings.LastIndex(text, "```"); idx != -1 {
			text = text[:idx]
		}
		text = strings.TrimSpace(text)
	}

	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 || end < start {
		return ""
	}
	return text[start : end+1]
}

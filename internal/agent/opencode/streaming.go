package opencode

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
)

// streamParser turns opencode's `run --format json` JSONL event stream into
// agent.TranscriptEvents incrementally, so ExecuteCLI persists a transcript
// as each turn occurs (issue #257) instead of one coarse blob after the run.
// It mirrors internal/agent/codex's streaming parser, specialized to
// opencode's event vocabulary — one JSON object per stdout line, each with a
// top-level "type" and a "part" payload:
//
//	{"type":"step_start","part":{"type":"step-start",…}}
//	{"type":"text","part":{"type":"text","text":"hi",…}}
//	{"type":"tool","part":{"type":"tool","tool":"bash","callID":"…","state":{"status":"completed","input":{…},"output":"…"}}}
//	{"type":"step_finish","part":{"type":"step-finish","tokens":{…},"cost":0}}
//
// Lifecycle events (step_start / step_finish) and non-JSON banner lines are
// ignored. The result envelope Forge instructs the backend to emit (a fenced
// ```json block, see clicommon.ResultContract) arrives as opencode's final
// text part(s), so Result returns the concatenated text-part text for
// ParseStructuredResult.
type streamParser struct {
	// message accumulates text-part text (the envelope lives in the final
	// one); tool events are streamed but not folded in.
	message strings.Builder
}

// newStreamParser returns a clicommon.StreamParser for one opencode invocation.
func newStreamParser() *streamParser { return &streamParser{} }

// opencodeEvent is the outer envelope of one opencode JSONL line. Only the
// fields Forge maps to transcript events are decoded; everything else is
// ignored so an opencode version that adds fields does not break parsing.
type opencodeEvent struct {
	Type string        `json:"type"`
	Part *opencodePart `json:"part"`
}

// opencodePart is the "part" payload carried by every opencode event. Type
// selects which fields are meaningful (text for text parts; tool/callID/state
// for tool parts).
type opencodePart struct {
	ID     string             `json:"id"`
	Type   string             `json:"type"`
	Text   string             `json:"text"`
	Tool   string             `json:"tool"`
	CallID string             `json:"callID"`
	State  *opencodeToolState `json:"state"`
}

// opencodeToolState is the "state" payload of a tool part: its status plus the
// tool's bounded input (an object) and output (a string once completed).
type opencodeToolState struct {
	Status string          `json:"status"`
	Input  json.RawMessage `json:"input"`
	Output string          `json:"output"`
}

// Line implements clicommon.StreamParser. A non-JSON or unrecognized line
// yields no events (opencode banner text, step lifecycle markers).
func (p *streamParser) Line(line string) []agent.TranscriptEvent {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return nil
	}
	var ev opencodeEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return nil
	}
	if ev.Part == nil {
		return nil
	}

	// Prefer the outer type, falling back to the part type; opencode sets
	// both consistently but the part type is the authoritative discriminator.
	kind := ev.Type
	if kind == "" {
		kind = ev.Part.Type
	}

	switch {
	case kind == "text" || ev.Part.Type == "text":
		if p.message.Len() > 0 {
			p.message.WriteString("\n")
		}
		p.message.WriteString(ev.Part.Text)
		return []agent.TranscriptEvent{{
			Type: agent.TranscriptEventMessage,
			Role: "assistant",
			Text: ev.Part.Text,
		}}
	case kind == "tool" || ev.Part.Type == "tool":
		return p.toolEvents(ev.Part, line)
	case kind == "step_start" || kind == "step_finish" ||
		ev.Part.Type == "step-start" || ev.Part.Type == "step-finish":
		// Lifecycle markers with no transcript content.
		return nil
	default:
		// Preserve any unrecognized part verbatim so nothing the Agent did is
		// silently dropped (mirrors codex's default case).
		return []agent.TranscriptEvent{{
			Type:       agent.TranscriptEventToolResult,
			Role:       "user",
			ToolName:   ev.Part.Type,
			ToolCallID: ev.Part.ID,
			ToolOutput: line,
		}}
	}
}

// toolEvents maps a tool part to its transcript events: a tool call when the
// state indicates it started/running, and a tool result once it has completed
// (or errored). The result output is the state's captured output, falling back
// to a compact JSON of its input when no output is present.
func (p *streamParser) toolEvents(part *opencodePart, raw string) []agent.TranscriptEvent {
	state := part.State
	if state == nil {
		// No state to interpret; preserve the raw line so nothing is dropped.
		return []agent.TranscriptEvent{{
			Type:       agent.TranscriptEventToolResult,
			Role:       "user",
			ToolName:   part.Tool,
			ToolCallID: part.CallID,
			ToolOutput: raw,
		}}
	}

	switch state.Status {
	case "running", "pending":
		return []agent.TranscriptEvent{{
			Type:       agent.TranscriptEventToolCall,
			Role:       "assistant",
			ToolName:   part.Tool,
			ToolCallID: part.CallID,
			ToolInput:  toolInput(state),
		}}
	default:
		// completed, error, or any terminal status: a tool result.
		return []agent.TranscriptEvent{{
			Type:       agent.TranscriptEventToolResult,
			Role:       "user",
			ToolName:   part.Tool,
			ToolCallID: part.CallID,
			ToolOutput: toolOutput(state),
		}}
	}
}

// toolInput renders a tool state's input as compact JSON, or empty when absent.
func toolInput(state *opencodeToolState) string {
	if len(state.Input) == 0 {
		return ""
	}
	return string(compactJSON(state.Input))
}

// toolOutput is the tool state's captured output, falling back to a compact
// JSON of its input when opencode reported no output.
func toolOutput(state *opencodeToolState) string {
	if state.Output != "" {
		return state.Output
	}
	return toolInput(state)
}

// compactJSON strips insignificant whitespace from a JSON value, returning the
// input unchanged if it is not valid JSON.
func compactJSON(raw json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return raw
	}
	return json.RawMessage(buf.Bytes())
}

// Result implements clicommon.StreamParser: the concatenated text-part text
// ExecuteCLI scans for the fenced result envelope.
func (p *streamParser) Result() string { return p.message.String() }

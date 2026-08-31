package pi

import (
	"encoding/json"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
)

// streamParser turns pi's `--mode json` JSONL event stream into
// agent.TranscriptEvents incrementally, so ExecuteCLI persists a transcript
// as each turn occurs (issue #257) instead of one coarse blob after the run.
// It mirrors internal/agent/codex's streaming parser, specialized to pi's
// event vocabulary:
//
//	{"type":"session","version":3,"id":"…"}
//	{"type":"agent_start"}
//	{"type":"turn_start"}
//	{"type":"message_start","message":{"role":"user","content":[…]}}
//	{"type":"message_end","message":{"role":"user","content":[…]}}
//	{"type":"message_start","message":{"role":"assistant","content":[]}}
//	{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hi"}}
//	{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"stopReason":"stop"}}
//
// pi streams token deltas via message_update.assistantMessageEvent
// (thinking_start/thinking_delta/text_delta/…); emitting one transcript event
// per delta would flood the bounded transcript with fragments, so those are
// ignored for emission. Instead one TranscriptEventMessage is emitted per
// assistant message_end, carrying the concatenated text of message.content.
// user-role messages (the prompt echo) and the session/agent_start/turn_start
// lifecycle markers carry no assistant content and are ignored. The result
// envelope Forge instructs the backend to emit (a fenced ```json block, see
// clicommon.ResultContract) arrives as the final assistant message, so Result
// returns the concatenated assistant message text for ParseStructuredResult.
type streamParser struct {
	// message accumulates assistant message_end text (the envelope lives in
	// the final one), newline-joined across turns.
	message strings.Builder
}

// newStreamParser returns a clicommon.StreamParser for one pi invocation.
func newStreamParser() *streamParser { return &streamParser{} }

// piEvent is the outer envelope of one pi JSONL line. Only the fields Forge
// maps to transcript events are decoded; everything else is ignored so a pi
// version that adds fields does not break parsing.
type piEvent struct {
	Type    string     `json:"type"`
	Message *piMessage `json:"message"`
}

// piMessage is the "message" payload carried by message_start / message_end
// events. Role selects whether the message is the prompt echo (user) or the
// Agent's own output (assistant); Content holds the message's blocks.
type piMessage struct {
	Role    string            `json:"role"`
	Content []json.RawMessage `json:"content"`
}

// piBlock is the type discriminator shared by every content block. Only Type
// and Text are decoded here; a non-text block is preserved verbatim from its
// raw JSON so nothing the Agent produced is silently dropped.
type piBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Input      json.RawMessage `json:"input"`
	ToolUseID  string          `json:"toolUseId"`
	ToolCallID string          `json:"toolCallId"`
}

// Line implements clicommon.StreamParser. A non-JSON or unrecognized line
// yields no events (pi's session/agent_start/turn_start lifecycle markers,
// message_start/message_update deltas, and user-role prompt echoes).
func (p *streamParser) Line(line string) []agent.TranscriptEvent {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return nil
	}
	var ev piEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return nil
	}

	// Only assistant message_end carries a completed turn's content. Deltas
	// (message_start/message_update) and user-role messages (the prompt echo)
	// are ignored, as are session/agent_start/turn_start lifecycle markers.
	if ev.Type != "message_end" || ev.Message == nil || ev.Message.Role != "assistant" {
		return nil
	}
	return p.assistantMessage(ev.Message)
}

// assistantMessage maps a completed assistant message to its transcript
// events: text blocks are joined into one assistant MESSAGE (and folded into
// the reconstructed result); a tool-use block becomes a TOOL_CALL and a
// tool-result block a TOOL_RESULT; any other block is preserved verbatim as a
// TOOL_RESULT so nothing the Agent did is silently dropped.
func (p *streamParser) assistantMessage(msg *piMessage) []agent.TranscriptEvent {
	var text strings.Builder
	var events []agent.TranscriptEvent

	for _, raw := range msg.Content {
		var block piBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			continue
		}
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use", "tool_call":
			events = append(events, agent.TranscriptEvent{
				Type:       agent.TranscriptEventToolCall,
				Role:       "assistant",
				ToolName:   block.Name,
				ToolCallID: firstNonEmpty(block.ID, block.ToolUseID, block.ToolCallID),
				ToolInput:  string(block.Input),
			})
		case "tool_result":
			events = append(events, agent.TranscriptEvent{
				Type:       agent.TranscriptEventToolResult,
				Role:       "user",
				ToolName:   block.Name,
				ToolCallID: firstNonEmpty(block.ToolUseID, block.ToolCallID, block.ID),
				ToolOutput: block.Text,
			})
		default:
			// An unrecognized block is preserved verbatim rather than dropped.
			events = append(events, agent.TranscriptEvent{
				Type:       agent.TranscriptEventToolResult,
				Role:       "user",
				ToolName:   block.Type,
				ToolCallID: block.ID,
				ToolOutput: string(raw),
			})
		}
	}

	joined := text.String()
	if joined != "" {
		if p.message.Len() > 0 {
			p.message.WriteString("\n")
		}
		p.message.WriteString(joined)
		// The assistant MESSAGE leads its turn's tool events.
		events = append([]agent.TranscriptEvent{{
			Type: agent.TranscriptEventMessage,
			Role: "assistant",
			Text: joined,
		}}, events...)
	}
	return events
}

// firstNonEmpty returns the first non-empty string, used to pick whichever
// id field a pi tool block populated.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Result implements clicommon.StreamParser: the concatenated assistant
// message text ExecuteCLI scans for the fenced result envelope.
func (p *streamParser) Result() string { return p.message.String() }

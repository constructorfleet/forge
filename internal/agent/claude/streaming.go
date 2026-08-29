package claude

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/textcap"
)

// maxTranscriptFieldBytes bounds how much of a single message/tool-call/
// tool-result field a TranscriptEvent retains — the same tail-preserving
// cap (internal/textcap) applied to stdout/stderr capture elsewhere in
// this package (see maxCapturedOutputLen): a chatty tool result must not
// force Forge to hold or persist unbounded text.
const maxTranscriptFieldBytes = 4000

// boundedTranscriptField caps s to maxTranscriptFieldBytes, keeping the
// tail (textcap.TailWriter) so the most recent/relevant part of a message
// or tool output survives truncation.
func boundedTranscriptField(s string) string {
	if len(s) <= maxTranscriptFieldBytes {
		return s
	}
	w := textcap.NewTailWriter(maxTranscriptFieldBytes)
	_, _ = w.Write([]byte(s))
	return w.String()
}

// streamLine is one line of `claude -p --output-format stream-json
// --verbose` output: either an assistant/user turn wrapping an Anthropic
// Message, or a terminal "result" line carrying the same final text `-p`
// alone would have printed.
type streamLine struct {
	Type    string         `json:"type"`
	Message *streamMessage `json:"message"`
	Result  string         `json:"result"`
}

type streamMessage struct {
	Role    string               `json:"role"`
	Content []streamContentBlock `json:"content"`
}

type streamContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

// parseStreamTranscript parses stdout as newline-delimited `claude
// --output-format stream-json --verbose` output, emitting a TranscriptEvent
// to sink (when non-nil) for every assistant message and tool call/result
// it recognizes, and returns the final response text — the same text `-p`
// alone would have printed — for parseStructuredResult to scan for the
// {status, summary} envelope.
//
// ok is false when stdout contains no recognizable stream-json lines at
// all (e.g. an older CLI, or a test double emitting plain text), signaling
// callers to fall back to treating stdout itself as the final text — the
// best-effort degradation ticket 28 requires: a streaming-parse miss never
// fails the run, it just forfeits transcript capture for that attempt.
func parseStreamTranscript(stdout string, sink agent.TranscriptSink, now func() time.Time) (finalText string, ok bool) {
	var assistantText strings.Builder
	var resultText string
	haveResult := false
	parsedAny := false

	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var sl streamLine
		if err := json.Unmarshal([]byte(line), &sl); err != nil {
			continue
		}

		// Only a recognized "type" counts as evidence this is genuinely
		// `--output-format stream-json` output — an arbitrary JSON object
		// (e.g. the {status, summary} envelope Claude Code is instructed
		// to emit inside a fenced block) parses fine as a streamLine too,
		// with Type == "". Counting that as "parsed" would silently
		// swallow the fenced result block into an empty finalText instead
		// of falling back to raw stdout.
		switch sl.Type {
		case "assistant":
			parsedAny = true
			emitAssistantBlocks(sl.Message, sink, now, &assistantText)
		case "user":
			parsedAny = true
			emitToolResultBlocks(sl.Message, sink, now)
		case "result":
			parsedAny = true
			resultText = sl.Result
			haveResult = true
		case "system":
			parsedAny = true
		}
	}

	if !parsedAny {
		return "", false
	}
	if haveResult {
		return resultText, true
	}
	return assistantText.String(), true
}

func emitAssistantBlocks(msg *streamMessage, sink agent.TranscriptSink, now func() time.Time, acc *strings.Builder) {
	if msg == nil {
		return
	}
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			if block.Text == "" {
				continue
			}
			acc.WriteString(block.Text)
			acc.WriteString("\n")
			emit(sink, agent.TranscriptEvent{
				Type:      agent.TranscriptEventMessage,
				Role:      "assistant",
				Text:      boundedTranscriptField(block.Text),
				Timestamp: now(),
			})
		case "tool_use":
			emit(sink, agent.TranscriptEvent{
				Type:      agent.TranscriptEventToolCall,
				Role:      "assistant",
				ToolName:  block.Name,
				ToolInput: boundedTranscriptField(string(block.Input)),
				Timestamp: now(),
			})
		}
	}
}

func emitToolResultBlocks(msg *streamMessage, sink agent.TranscriptSink, now func() time.Time) {
	if msg == nil {
		return
	}
	for _, block := range msg.Content {
		if block.Type != "tool_result" {
			continue
		}
		emit(sink, agent.TranscriptEvent{
			Type:       agent.TranscriptEventToolResult,
			Role:       "user",
			ToolName:   block.ToolUseID,
			ToolOutput: boundedTranscriptField(toolResultText(block.Content)),
			Timestamp:  now(),
		})
	}
}

// toolResultText extracts human-readable text from a tool_result content
// block, whose JSON shape varies by tool: usually a plain JSON string, but
// sometimes a nested content-block array. Unrecognized shapes fall back to
// the raw JSON so nothing is silently dropped.
func toolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []streamContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, block := range blocks {
			if block.Type == "text" {
				b.WriteString(block.Text)
			}
		}
		if b.Len() > 0 {
			return b.String()
		}
	}
	return string(raw)
}

// emit is a nil-safe convenience so callers don't have to guard every call
// site against a nil sink (the common case when no caller opted into
// transcript capture).
func emit(sink agent.TranscriptSink, event agent.TranscriptEvent) {
	if sink == nil {
		return
	}
	sink.Emit(event)
}

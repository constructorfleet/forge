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
// alone would have printed. Timestamp is the backend's own per-event clock
// (issue 36) — the real time the event occurred, used in preference to
// Forge's parse-time clock so inter-event durations are meaningful.
type streamLine struct {
	Type    string         `json:"type"`
	Subtype string         `json:"subtype"`
	IsError bool           `json:"is_error"`
	Message *streamMessage `json:"message"`
	Result  string         `json:"result"`
	// Timestamp is the backend's own per-event clock (issue 36).
	Timestamp string `json:"timestamp"`
	// Model, CWD, and SessionID are carried on the leading "system"/"init"
	// line (issue 36) and folded into a summary TranscriptEvent so a
	// transcript's first recorded event is the run's actual start, not an
	// orphaned mid-run tool result.
	Model     string `json:"model"`
	CWD       string `json:"cwd"`
	SessionID string `json:"session_id"`
}

type streamMessage struct {
	Role    string               `json:"role"`
	Content []streamContentBlock `json:"content"`
}

type streamContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

// streamParser consumes `claude --output-format stream-json --verbose`
// output one line at a time (issue 36: incrementally, as the subprocess
// emits it, rather than re-parsing a captured buffer after the fact) and
// emits a TranscriptEvent to sink for every assistant message and tool
// call/result it recognizes. It accumulates the final response text — the
// same text `-p` alone would have printed — for parseStructuredResult to
// scan for the {status, summary} envelope.
//
// Parsing is best-effort by ticket 28's contract: a malformed line, or a
// panic from a sink's Emit, must never fail or hang the Agent's run. consume
// recovers from any panic and sets aborted; the Adapter then falls back to
// treating raw stdout as the final text (its pre-transcript behavior),
// forfeiting transcript capture for that attempt rather than propagating the
// failure.
type streamParser struct {
	sink agent.TranscriptSink
	now  func() time.Time

	assistant  strings.Builder
	resultText string
	haveResult bool
	parsedAny  bool
	aborted    bool

	// resultIsError and resultSubtype capture the terminal "result" line's
	// is_error/subtype fields (issue 20/ticket 32): a CLI-level failure —
	// e.g. an error subtype such as "error_max_turns" or
	// "error_during_execution", or a permission-request the unattended run
	// couldn't satisfy — is distinct from "the model's final text didn't
	// conform to resultJSONSchema" and must be diagnosable as such by
	// Execute, without waiting to attempt result parsing first.
	resultIsError bool
	resultSubtype string

	// callNames maps a tool-use id to the tool's name, recorded when a
	// TOOL_CALL is seen so its later TOOL_RESULT can be labelled with the
	// real tool name (not just the opaque id) and paired without orphans.
	callNames map[string]string
}

func newStreamParser(sink agent.TranscriptSink, now func() time.Time) *streamParser {
	if now == nil {
		now = time.Now
	}
	return &streamParser{sink: sink, now: now, callNames: make(map[string]string)}
}

// consume parses one line of stream-json output, emitting any recognized
// events to the sink. It is panic-safe: a sink whose Emit panics, or an
// unexpected malformation, sets aborted and is otherwise swallowed, so a
// capture bug can never change the Agent's outcome.
func (p *streamParser) consume(line string) {
	if p.aborted {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			p.aborted = true
		}
	}()

	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	var sl streamLine
	if err := json.Unmarshal([]byte(line), &sl); err != nil {
		return
	}

	// Only a recognized "type" counts as evidence this is genuinely
	// `--output-format stream-json` output — an arbitrary JSON object
	// (e.g. the {status, summary} envelope Claude Code is instructed to
	// emit inside a fenced block) parses fine as a streamLine too, with
	// Type == "". Counting that as "parsed" would silently swallow the
	// fenced result block into an empty finalText instead of falling back
	// to raw stdout.
	ts := parseEventTime(sl.Timestamp, p.now)
	switch sl.Type {
	case "assistant":
		p.parsedAny = true
		p.emitAssistantBlocks(sl.Message, ts)
	case "user":
		p.parsedAny = true
		p.emitToolResultBlocks(sl.Message, ts)
	case "result":
		p.parsedAny = true
		p.resultText = sl.Result
		p.haveResult = true
		p.resultIsError = sl.IsError
		p.resultSubtype = sl.Subtype
	case "system":
		p.parsedAny = true
		p.emitSystemInit(sl, ts)
	}
}

// emitSystemInit emits a summary TranscriptEvent for the stream's leading
// "system"/"init" line (issue 36): the transcript's captured turn sequence
// must start at the run's actual beginning, not at whatever the first
// tool call happens to be, and re-deriving the CLI's full init payload
// isn't necessary for that — a compact summary is enough to anchor the
// timeline and record which model/session produced it.
func (p *streamParser) emitSystemInit(sl streamLine, ts time.Time) {
	if sl.Subtype != "init" {
		return
	}
	summary := "session initialized"
	if sl.Model != "" {
		summary += " model=" + sl.Model
	}
	if sl.CWD != "" {
		summary += " cwd=" + sl.CWD
	}
	if sl.SessionID != "" {
		summary += " session_id=" + sl.SessionID
	}
	p.emit(agent.TranscriptEvent{
		Type:      agent.TranscriptEventMessage,
		Role:      "system",
		Text:      boundedTranscriptField(summary),
		Timestamp: ts,
	})
}

// finalText returns the reconstructed final response text and whether the
// stream was recognized at all. ok is false when no stream-json line was
// seen (e.g. an older CLI or a plain-text test double), signalling the
// caller to fall back to raw stdout.
func (p *streamParser) finalText() (string, bool) {
	if !p.parsedAny {
		return "", false
	}
	if p.haveResult {
		return p.resultText, true
	}
	return p.assistant.String(), true
}

// resultError reports whether the terminal "result" line the CLI emitted
// was itself flagged as an error (is_error: true), and the subtype it
// carried, if any. It only reflects a genuine stream-json result line
// (p.aborted or a non-stream run both leave it false/""), so a
// capture-abort or non-stream fallback never fabricates a spurious error.
func (p *streamParser) resultError() (isError bool, subtype string) {
	if p.aborted {
		return false, ""
	}
	return p.resultIsError, p.resultSubtype
}

// reconstructedFinalText resolves the final text the same way the Adapter's
// pre-transcript code did: the streamed result when the stream was
// recognized and capture wasn't aborted, else raw stdout unchanged. Keeping
// this decision in one place means a streaming-parse miss or a sink panic
// both degrade to exactly the old behavior.
func (p *streamParser) reconstructedFinalText(rawStdout string) string {
	if p.aborted {
		return rawStdout
	}
	if text, ok := p.finalText(); ok {
		return text
	}
	return rawStdout
}

func (p *streamParser) emitAssistantBlocks(msg *streamMessage, ts time.Time) {
	if msg == nil {
		return
	}
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			if block.Text == "" {
				continue
			}
			p.assistant.WriteString(block.Text)
			p.assistant.WriteString("\n")
			p.emit(agent.TranscriptEvent{
				Type:      agent.TranscriptEventMessage,
				Role:      "assistant",
				Text:      boundedTranscriptField(block.Text),
				Timestamp: ts,
			})
		case "tool_use":
			if block.ID != "" {
				p.callNames[block.ID] = block.Name
			}
			p.emit(agent.TranscriptEvent{
				Type:       agent.TranscriptEventToolCall,
				Role:       "assistant",
				ToolName:   block.Name,
				ToolCallID: block.ID,
				ToolInput:  boundedTranscriptField(string(block.Input)),
				Timestamp:  ts,
			})
		}
	}
}

func (p *streamParser) emitToolResultBlocks(msg *streamMessage, ts time.Time) {
	if msg == nil {
		return
	}
	for _, block := range msg.Content {
		if block.Type != "tool_result" {
			continue
		}
		p.emit(agent.TranscriptEvent{
			Type:       agent.TranscriptEventToolResult,
			Role:       "user",
			ToolName:   p.callNames[block.ToolUseID],
			ToolCallID: block.ToolUseID,
			ToolOutput: boundedTranscriptField(toolResultText(block.Content)),
			Timestamp:  ts,
		})
	}
}

func (p *streamParser) emit(event agent.TranscriptEvent) {
	if p.sink == nil {
		return
	}
	p.sink.Emit(event)
}

// parseEventTime returns the backend's own event timestamp when present and
// parseable (RFC 3339, with or without sub-second precision), falling back
// to Forge's clock so an event always has a time even if the backend omits
// or malforms one (issue 36).
func parseEventTime(raw string, now func() time.Time) time.Time {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			return t
		}
	}
	return now()
}

// parseStreamTranscript parses stdout as newline-delimited `claude
// --output-format stream-json --verbose` output, emitting a TranscriptEvent
// to sink (when non-nil) for every assistant message and tool call/result
// it recognizes, and returns the final response text — the same text `-p`
// alone would have printed — for parseStructuredResult to scan for the
// {status, summary} envelope. It is the batch entry point over a complete
// captured buffer; Adapter.Execute feeds the parser incrementally instead.
//
// ok is false when stdout contains no recognizable stream-json lines at all
// (e.g. an older CLI, or a test double emitting plain text), signaling
// callers to fall back to treating stdout itself as the final text — the
// best-effort degradation ticket 28 requires: a streaming-parse miss never
// fails the run, it just forfeits transcript capture for that attempt.
func parseStreamTranscript(stdout string, sink agent.TranscriptSink, now func() time.Time) (finalText string, ok bool) {
	p := newStreamParser(sink, now)
	for _, line := range strings.Split(stdout, "\n") {
		p.consume(line)
	}
	return p.finalText()
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

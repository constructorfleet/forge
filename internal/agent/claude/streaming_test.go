package claude

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
)

// scriptedStreamTranscript is a representative `claude -p --output-format
// stream-json --verbose` transcript: an assistant text turn, a tool call,
// its tool result, a second assistant text turn, and a terminal "result"
// line carrying the final response text (including the {status, summary}
// envelope) — the same text `-p` alone would have printed.
const scriptedStreamTranscript = `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Let me check the build."}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"go build ./..."}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"build ok"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Build passed."}]}}
{"type":"result","subtype":"success","result":"Build passed.\n\n` + "```json\\n" + `{\"status\":\"IMPLEMENTED\",\"summary\":\"Fixed the build.\"}\n` + "```" + `"}
`

func TestParseStreamTranscript_EmitsEventsAndReturnsFinalText(t *testing.T) {
	recorder := agent.NewTranscriptRecorder()
	fixedNow := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

	finalText, ok := parseStreamTranscript(scriptedStreamTranscript, recorder, func() time.Time { return fixedNow })
	if !ok {
		t.Fatalf("parseStreamTranscript reported ok=false, want true")
	}
	if !strings.Contains(finalText, `"status":"IMPLEMENTED"`) {
		t.Fatalf("finalText = %q, want the structured result envelope", finalText)
	}

	events := recorder.Events()
	if len(events) != 5 {
		t.Fatalf("got %d events, want 5 (system init, 2 messages, 1 tool call, 1 tool result): %+v", len(events), events)
	}

	if events[0].Type != agent.TranscriptEventMessage || events[0].Role != "system" {
		t.Fatalf("events[0] = %+v, want the system/init summary", events[0])
	}
	if events[1].Type != agent.TranscriptEventMessage || events[1].Text != "Let me check the build." {
		t.Fatalf("events[1] = %+v, want the first assistant message", events[1])
	}
	if events[2].Type != agent.TranscriptEventToolCall || events[2].ToolName != "Bash" {
		t.Fatalf("events[2] = %+v, want a Bash tool call", events[2])
	}
	if !strings.Contains(events[2].ToolInput, "go build") {
		t.Fatalf("events[2].ToolInput = %q, want the tool input captured", events[2].ToolInput)
	}
	if events[3].Type != agent.TranscriptEventToolResult || events[3].ToolOutput != "build ok" {
		t.Fatalf("events[3] = %+v, want the tool result", events[3])
	}
	if events[4].Type != agent.TranscriptEventMessage || events[4].Text != "Build passed." {
		t.Fatalf("events[4] = %+v, want the second assistant message", events[4])
	}

	for i, event := range events {
		if event.Seq != i {
			t.Fatalf("events[%d].Seq = %d, want %d", i, event.Seq, i)
		}
		if !event.Timestamp.Equal(fixedNow) {
			t.Fatalf("events[%d].Timestamp = %v, want %v", i, event.Timestamp, fixedNow)
		}
	}
}

// TestParseStreamTranscript_PerEventTimestampsAreDistinctAndMonotonic is
// issue 36's core fix: occurred_at must come from each stream event's own
// timestamp, not a single persist-time stamp shared by every row. A real
// `claude --output-format stream-json` transcript carries a distinct
// RFC3339Nano timestamp per line; this asserts every parsed event's
// Timestamp reflects its own line, in increasing order.
func TestParseStreamTranscript_PerEventTimestampsAreDistinctAndMonotonic(t *testing.T) {
	transcript := `{"type":"system","subtype":"init","timestamp":"2026-08-28T12:00:00.000Z"}
{"type":"assistant","timestamp":"2026-08-28T12:00:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Let me check the build."}]}}
{"type":"assistant","timestamp":"2026-08-28T12:00:02.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"go build ./..."}}]}}
{"type":"user","timestamp":"2026-08-28T12:00:07.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"build ok"}]}}
{"type":"result","subtype":"success","result":"done"}
`
	recorder := agent.NewTranscriptRecorder()
	if _, ok := parseStreamTranscript(transcript, recorder, time.Now); !ok {
		t.Fatalf("parseStreamTranscript reported ok=false, want true")
	}

	events := recorder.Events()
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4: %+v", len(events), events)
	}
	for i := 1; i < len(events); i++ {
		if !events[i].Timestamp.After(events[i-1].Timestamp) {
			t.Fatalf("events[%d].Timestamp = %v, want strictly after events[%d].Timestamp = %v", i, events[i].Timestamp, i-1, events[i-1].Timestamp)
		}
	}
	if got := events[2].Timestamp.Sub(events[1].Timestamp); got != time.Second {
		t.Fatalf("events[2].Timestamp - events[1].Timestamp = %v, want 1s (the real inter-event gap from the stream)", got)
	}
	if got := events[3].Timestamp.Sub(events[2].Timestamp); got != 5*time.Second {
		t.Fatalf("events[3].Timestamp - events[2].Timestamp = %v, want 5s (the real inter-event gap from the stream)", got)
	}
}

// TestParseStreamTranscript_NoDroppedLeadingEventsAndCallResultPairing is
// issue 36's other core fix: capture starts at the stream's actual
// beginning (a system/init summary, then the first assistant message and
// every tool call), and every TOOL_RESULT is paired to its TOOL_CALL by id
// with no orphans.
func TestParseStreamTranscript_NoDroppedLeadingEventsAndCallResultPairing(t *testing.T) {
	recorder := agent.NewTranscriptRecorder()
	if _, ok := parseStreamTranscript(scriptedStreamTranscript, recorder, time.Now); !ok {
		t.Fatalf("parseStreamTranscript reported ok=false, want true")
	}

	events := recorder.Events()
	if len(events) != 5 {
		t.Fatalf("got %d events, want 5", len(events))
	}
	if events[0].Type != agent.TranscriptEventMessage || events[0].Role != "system" {
		t.Fatalf("events[0] = %+v, want the leading system/init summary — no dropped leading events", events[0])
	}
	call, result := events[2], events[3]
	if call.Type != agent.TranscriptEventToolCall || call.ToolCallID != "tool-1" {
		t.Fatalf("call = %+v, want TOOL_CALL with ToolCallID tool-1", call)
	}
	if result.Type != agent.TranscriptEventToolResult || result.ToolCallID != "tool-1" {
		t.Fatalf("result = %+v, want TOOL_RESULT paired to the same ToolCallID (no orphan)", result)
	}
	if result.ToolName != call.ToolName {
		t.Fatalf("result.ToolName = %q, want it resolved from the call it pairs to (%q)", result.ToolName, call.ToolName)
	}
}

func TestParseStreamTranscript_NonStreamOutputDegradesGracefully(t *testing.T) {
	recorder := agent.NewTranscriptRecorder()
	plain := "I implemented the change.\n\n```json\n" +
		`{"status":"IMPLEMENTED","summary":"Added the feature."}` +
		"\n```\n"

	finalText, ok := parseStreamTranscript(plain, recorder, time.Now)
	if ok {
		t.Fatalf("parseStreamTranscript reported ok=true for non-stream-json output")
	}
	if finalText != "" {
		t.Fatalf("finalText = %q, want empty when ok=false (caller falls back to raw stdout)", finalText)
	}
	if events := recorder.Events(); len(events) != 0 {
		t.Fatalf("got %d events for non-stream-json output, want 0: %+v", len(events), events)
	}
}

func TestParseStreamTranscript_NilSinkIsSafe(t *testing.T) {
	finalText, ok := parseStreamTranscript(scriptedStreamTranscript, nil, time.Now)
	if !ok {
		t.Fatalf("parseStreamTranscript reported ok=false, want true")
	}
	if !strings.Contains(finalText, `"status":"IMPLEMENTED"`) {
		t.Fatalf("finalText = %q, want the structured result envelope", finalText)
	}
}

func TestParseStreamTranscript_LongFieldsAreBounded(t *testing.T) {
	recorder := agent.NewTranscriptRecorder()
	longText := strings.Repeat("x", maxTranscriptFieldBytes*2)
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + longText + `"}]}}` + "\n" +
		`{"type":"result","subtype":"success","result":"done"}`

	if _, ok := parseStreamTranscript(line, recorder, time.Now); !ok {
		t.Fatalf("parseStreamTranscript reported ok=false, want true")
	}
	events := recorder.Events()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if len(events[0].Text) > maxTranscriptFieldBytes+64 {
		t.Fatalf("events[0].Text len = %d, want bounded near maxTranscriptFieldBytes (%d)", len(events[0].Text), maxTranscriptFieldBytes)
	}
}

// TestExecute_StreamingTranscriptFeedsSinkAndStructuredResult is the
// end-to-end version of the two tests above through Adapter.Execute: a
// scripted stream-json Runner output both populates req.Transcript and
// still parses to the correct AgentResult.
func TestExecute_StreamingTranscriptFeedsSinkAndStructuredResult(t *testing.T) {
	var calls []recordedCall
	a := &Adapter{Runner: newFakeRunner(&calls, scriptedStreamTranscript, "", 0, nil)}

	recorder := agent.NewTranscriptRecorder()
	req := baseRequest()
	req.Transcript = recorder

	result, err := a.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusImplemented {
		t.Fatalf("Status = %q, want IMPLEMENTED", result.Status)
	}
	if result.Summary != "Fixed the build." {
		t.Fatalf("Summary = %q", result.Summary)
	}
	if len(recorder.Events()) != 5 {
		t.Fatalf("got %d transcript events, want 5", len(recorder.Events()))
	}
}

// TestExecute_TranscriptCaptureNeverChangesOutcome verifies ticket 28's
// degrade-gracefully requirement at the Execute boundary: a sink whose
// Emit panics must not crash or hang Execute — a streaming-parse/sink bug
// degrades to the pre-transcript behavior (treating raw stdout as the
// final text) rather than propagating a panic out of Execute.
func TestExecute_TranscriptCaptureNeverChangesOutcome(t *testing.T) {
	var calls []recordedCall
	// A recognizable stream-json "assistant" line (so Emit actually fires
	// and panics) followed, as plain literal text with real newlines, by
	// the fenced result block — proving the panic-recovery fallback (raw
	// stdout) still lets parseStructuredResult find it afterward.
	stdout := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}` + "\n" +
		"```json\n" + `{"status":"IMPLEMENTED","summary":"done"}` + "\n```\n"
	a := &Adapter{Runner: newFakeRunner(&calls, stdout, "", 0, nil)}

	req := baseRequest()
	req.Transcript = panickingSink{}

	result, err := a.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != agent.StatusImplemented {
		t.Fatalf("Status = %q, want IMPLEMENTED: a panicking sink must degrade to raw-stdout parsing, not change the outcome", result.Status)
	}
	if result.Summary != "done" {
		t.Fatalf("Summary = %q, want the structured result recovered despite the panicking sink", result.Summary)
	}
}

type panickingSink struct{}

func (panickingSink) Emit(agent.TranscriptEvent) {
	panic("sink boom")
}

var _ agent.TranscriptSink = panickingSink{}

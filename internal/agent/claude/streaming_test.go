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
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4 (2 messages, 1 tool call, 1 tool result): %+v", len(events), events)
	}

	if events[0].Type != agent.TranscriptEventMessage || events[0].Text != "Let me check the build." {
		t.Fatalf("events[0] = %+v, want the first assistant message", events[0])
	}
	if events[1].Type != agent.TranscriptEventToolCall || events[1].ToolName != "Bash" {
		t.Fatalf("events[1] = %+v, want a Bash tool call", events[1])
	}
	if !strings.Contains(events[1].ToolInput, "go build") {
		t.Fatalf("events[1].ToolInput = %q, want the tool input captured", events[1].ToolInput)
	}
	if events[2].Type != agent.TranscriptEventToolResult || events[2].ToolOutput != "build ok" {
		t.Fatalf("events[2] = %+v, want the tool result", events[2])
	}
	if events[3].Type != agent.TranscriptEventMessage || events[3].Text != "Build passed." {
		t.Fatalf("events[3] = %+v, want the second assistant message", events[3])
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
	if len(recorder.Events()) != 4 {
		t.Fatalf("got %d transcript events, want 4", len(recorder.Events()))
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

package opencode

import (
	"testing"

	"github.com/Teagan42/forge/internal/agent"
)

// feed runs each line through the parser and returns every emitted event, in
// order, mirroring how clicommon.ExecuteCLI drives it via onLine.
func feed(p *streamParser, lines []string) []agent.TranscriptEvent {
	var got []agent.TranscriptEvent
	for _, l := range lines {
		got = append(got, p.Line(l)...)
	}
	return got
}

// A real `opencode run --format json` transcript: a banner line, the step
// lifecycle markers (step_start / step_finish), a text part carrying the
// assistant message, and a completed tool part.
func TestStreamParser_RealOpencodeSession(t *testing.T) {
	lines := []string{
		"opencode v0.0.0 starting...",
		`{"type":"step_start","timestamp":1788170406163,"sessionID":"ses_1","part":{"id":"prt_1","messageID":"msg_1","type":"step-start"}}`,
		`{"type":"tool","timestamp":1788170407000,"sessionID":"ses_1","part":{"id":"prt_2","messageID":"msg_1","type":"tool","tool":"bash","callID":"call_1","state":{"status":"completed","input":{"command":"echo hi"},"output":"hi\n"}}}`,
		`{"type":"text","timestamp":1788170408654,"sessionID":"ses_1","part":{"id":"prt_3","messageID":"msg_1","type":"text","text":"hi","time":{"start":1,"end":2}}}`,
		`{"type":"step_finish","timestamp":1788170409000,"part":{"type":"step-finish","tokens":{"input":10,"output":2},"cost":0}}`,
	}
	p := newStreamParser()
	got := feed(p, lines)

	// The banner line and the step lifecycle markers yield nothing. The two
	// content events remain: the completed tool result and the assistant
	// message.
	if len(got) != 2 {
		t.Fatalf("emitted %d events, want 2: %+v", len(got), got)
	}
	if got[0].Type != agent.TranscriptEventToolResult || got[0].ToolCallID != "call_1" || got[0].ToolName != "bash" {
		t.Fatalf("event[0] = %+v, want a bash TOOL_RESULT for call_1", got[0])
	}
	if got[0].ToolOutput != "hi\n" {
		t.Fatalf("tool result output = %q, want %q", got[0].ToolOutput, "hi\n")
	}
	if got[1].Type != agent.TranscriptEventMessage || got[1].Role != "assistant" || got[1].Text != "hi" {
		t.Fatalf("event[1] = %+v, want assistant MESSAGE \"hi\"", got[1])
	}
}

// The result envelope is reconstructed from text-part text — a fenced block
// split across the JSONL as several text parts must survive joined.
func TestStreamParser_ResultReconstructsEnvelope(t *testing.T) {
	p := newStreamParser()
	feed(p, []string{
		`{"type":"text","part":{"type":"text","text":"here is the result:"}}`,
		"{\"type\":\"text\",\"part\":{\"type\":\"text\",\"text\":\"```json\\n{\\\"status\\\":\\\"IMPLEMENTED\\\",\\\"summary\\\":\\\"ok\\\"}\\n```\"}}",
	})
	got := p.Result()
	if !containsAll(got, `"status":"IMPLEMENTED"`, "```json") {
		t.Fatalf("Result() = %q, want the reconstructed fenced envelope", got)
	}
}

// A text part emits an assistant message and accumulates into the result.
func TestStreamParser_TextPartBecomesMessage(t *testing.T) {
	p := newStreamParser()
	got := feed(p, []string{
		`{"type":"text","part":{"type":"text","text":"hello"}}`,
	})
	if len(got) != 1 || got[0].Type != agent.TranscriptEventMessage || got[0].Role != "assistant" || got[0].Text != "hello" {
		t.Fatalf("want one assistant MESSAGE \"hello\", got %+v", got)
	}
	if p.Result() != "hello" {
		t.Fatalf("Result() = %q, want %q", p.Result(), "hello")
	}
}

// Non-JSON banner lines and step lifecycle markers yield no events.
func TestStreamParser_LifecycleAndBannerYieldNothing(t *testing.T) {
	p := newStreamParser()
	got := feed(p, []string{
		"opencode starting...",
		"",
		`{"type":"step_start","part":{"type":"step-start"}}`,
		`{"type":"step_finish","part":{"type":"step-finish","cost":0}}`,
	})
	if len(got) != 0 {
		t.Fatalf("want no events, got %+v", got)
	}
	if p.Result() != "" {
		t.Fatalf("Result() = %q, want empty", p.Result())
	}
}

// A running tool part emits a tool call carrying its compacted input.
func TestStreamParser_RunningToolEmitsCall(t *testing.T) {
	p := newStreamParser()
	got := feed(p, []string{
		`{"type":"tool","part":{"type":"tool","tool":"bash","callID":"c1","state":{"status":"running","input":{"command":"ls"}}}}`,
	})
	if len(got) != 1 || got[0].Type != agent.TranscriptEventToolCall || got[0].ToolName != "bash" || got[0].ToolCallID != "c1" {
		t.Fatalf("want one bash TOOL_CALL for c1, got %+v", got)
	}
	if !containsAll(got[0].ToolInput, `"command":"ls"`) {
		t.Fatalf("tool call input = %q, want the compacted input", got[0].ToolInput)
	}
}

// A completed tool with no output falls back to its compacted input so the
// call is not lossy.
func TestStreamParser_CompletedToolFallsBackToInput(t *testing.T) {
	p := newStreamParser()
	got := feed(p, []string{
		`{"type":"tool","part":{"type":"tool","tool":"read","callID":"c2","state":{"status":"completed","input":{"path":"a.go"}}}}`,
	})
	if len(got) != 1 || got[0].Type != agent.TranscriptEventToolResult {
		t.Fatalf("want one TOOL_RESULT, got %+v", got)
	}
	if !containsAll(got[0].ToolOutput, `"path":"a.go"`) {
		t.Fatalf("tool output = %q, want the compacted input fallback", got[0].ToolOutput)
	}
}

// An unrecognized part type is preserved verbatim rather than dropped.
func TestStreamParser_UnknownPartPreserved(t *testing.T) {
	p := newStreamParser()
	raw := `{"type":"reasoning","part":{"id":"r1","type":"reasoning","text":"thinking"}}`
	got := feed(p, []string{raw})
	if len(got) != 1 || got[0].Type != agent.TranscriptEventToolResult || got[0].ToolName != "reasoning" {
		t.Fatalf("want a preserved reasoning TOOL_RESULT, got %+v", got)
	}
	if got[0].ToolOutput != raw {
		t.Fatalf("unknown part not preserved verbatim: %q", got[0].ToolOutput)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

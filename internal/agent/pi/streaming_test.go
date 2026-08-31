package pi

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

// A real `pi --mode json` transcript: the session/agent/turn lifecycle
// markers, the user prompt echo (message_start + message_end), the assistant
// message_start, a run of token deltas (message_update), and the assistant
// message_end carrying the completed text. Only the assistant message_end
// yields a transcript event.
func TestStreamParser_RealPiSession(t *testing.T) {
	lines := []string{
		`{"type":"session","version":3,"id":"01a05741","timestamp":1,"cwd":"/workspace"}`,
		`{"type":"agent_start"}`,
		`{"type":"turn_start"}`,
		`{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"do the thing"}],"timestamp":1}}`,
		`{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"do the thing"}],"timestamp":1}}`,
		`{"type":"message_start","message":{"role":"assistant","content":[],"api":"x","provider":"y","model":"z","usage":{},"stopReason":"pending","timestamp":2}}`,
		`{"type":"message_update","usage":{},"assistantMessageEvent":{"type":"thinking_start","contentIndex":0}}`,
		`{"type":"message_update","usage":{},"assistantMessageEvent":{"type":"thinking_delta","contentIndex":0,"delta":"The"}}`,
		`{"type":"message_update","usage":{},"assistantMessageEvent":{"type":"text_delta","contentIndex":1,"delta":"hi"}}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"stopReason":"stop","timestamp":3}}`,
	}
	p := newStreamParser()
	got := feed(p, lines)

	// Lifecycle markers, the user prompt echo, the assistant message_start,
	// and every token delta yield nothing. Only the assistant message_end
	// remains, as one assistant MESSAGE with the joined content text.
	if len(got) != 1 {
		t.Fatalf("emitted %d events, want 1: %+v", len(got), got)
	}
	if got[0].Type != agent.TranscriptEventMessage || got[0].Role != "assistant" || got[0].Text != "hi" {
		t.Fatalf("event[0] = %+v, want assistant MESSAGE \"hi\"", got[0])
	}
}

// Multiple text blocks in one assistant message_end are joined into a single
// assistant MESSAGE.
func TestStreamParser_JoinsMultipleTextBlocks(t *testing.T) {
	p := newStreamParser()
	got := feed(p, []string{
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}],"stopReason":"stop"}}`,
	})
	if len(got) != 1 || got[0].Type != agent.TranscriptEventMessage || got[0].Text != "hello world" {
		t.Fatalf("got %+v, want one assistant MESSAGE \"hello world\"", got)
	}
}

// Token deltas, user messages, and lifecycle events emit nothing on their own.
func TestStreamParser_DeltasAndUserAndLifecycleEmitNothing(t *testing.T) {
	p := newStreamParser()
	got := feed(p, []string{
		`{"type":"session","version":3,"id":"x"}`,
		`{"type":"agent_start"}`,
		`{"type":"turn_start"}`,
		`{"type":"message_start","message":{"role":"assistant","content":[]}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hi"}}`,
		`{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"prompt"}]}}`,
		`{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"prompt"}]}}`,
		"not json at all",
		"",
	})
	if len(got) != 0 {
		t.Fatalf("emitted %d events, want 0: %+v", len(got), got)
	}
}

// The result envelope is reconstructed from assistant message_end text — a
// fenced ```json block carried in the final assistant message must survive.
func TestStreamParser_ResultReconstructsEnvelope(t *testing.T) {
	p := newStreamParser()
	feed(p, []string{
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"here is the result:"}],"stopReason":"stop"}}`,
		"{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"```json\\n{\\\"status\\\":\\\"IMPLEMENTED\\\",\\\"summary\\\":\\\"ok\\\"}\\n```\"}],\"stopReason\":\"stop\"}}",
	})
	got := p.Result()
	if !containsAll(got, `"status":"IMPLEMENTED"`, "```json") {
		t.Fatalf("Result() = %q, want the reconstructed fenced envelope", got)
	}
}

// A tool-use block becomes a TOOL_CALL; a non-text/unknown block is preserved
// verbatim as a TOOL_RESULT rather than dropped.
func TestStreamParser_ToolAndUnknownBlocksPreserved(t *testing.T) {
	p := newStreamParser()
	got := feed(p, []string{
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"shell","input":{"cmd":"ls"}},{"type":"mystery","foo":"bar"}],"stopReason":"tool_use"}}`,
	})
	if len(got) != 2 {
		t.Fatalf("emitted %d events, want 2: %+v", len(got), got)
	}
	if got[0].Type != agent.TranscriptEventToolCall || got[0].ToolCallID != "call_1" || got[0].ToolName != "shell" {
		t.Fatalf("event[0] = %+v, want a shell TOOL_CALL for call_1", got[0])
	}
	if got[1].Type != agent.TranscriptEventToolResult || got[1].ToolName != "mystery" {
		t.Fatalf("event[1] = %+v, want a preserved mystery TOOL_RESULT", got[1])
	}
	if !containsAll(got[1].ToolOutput, `"type":"mystery"`, `"foo":"bar"`) {
		t.Fatalf("unknown block not preserved verbatim: %q", got[1].ToolOutput)
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

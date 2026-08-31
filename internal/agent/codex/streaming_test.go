package codex

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

// A real `codex exec --json` transcript: banner lines, a command execution
// (started + completed), the final agent_message carrying the result
// envelope, and the turn.completed usage marker.
func TestStreamParser_RealCodexSession(t *testing.T) {
	lines := []string{
		"Reading prompt from stdin...",
		`{"type":"thread.started","thread_id":"01a0573e"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.started","item":{"id":"item_0","type":"command_execution","command":"/bin/zsh -lc 'echo hi'","aggregated_output":"","exit_code":null,"status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"/bin/zsh -lc 'echo hi'","aggregated_output":"hi\n","exit_code":0,"status":"completed"}}`,
		"```json",
		`{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"done"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":2}}`,
		"Shell cwd was reset to /Users/tglenn/src/forge",
	}
	p := newStreamParser()
	got := feed(p, lines)

	// Banner lines, the stray non-JSON "```json" line, and the lifecycle
	// markers (thread/turn) yield nothing. The three content events remain:
	// tool call, tool result, assistant message.
	if len(got) != 3 {
		t.Fatalf("emitted %d events, want 3: %+v", len(got), got)
	}
	if got[0].Type != agent.TranscriptEventToolCall || got[0].ToolCallID != "item_0" || got[0].ToolName != "shell" {
		t.Fatalf("event[0] = %+v, want a shell TOOL_CALL for item_0", got[0])
	}
	if got[0].ToolInput == "" {
		t.Fatalf("tool call carried no command: %+v", got[0])
	}
	if got[1].Type != agent.TranscriptEventToolResult || got[1].ToolCallID != "item_0" {
		t.Fatalf("event[1] = %+v, want a TOOL_RESULT paired to item_0", got[1])
	}
	if got[1].ToolOutput != "hi\n" {
		t.Fatalf("tool result output = %q, want %q", got[1].ToolOutput, "hi\n")
	}
	if got[2].Type != agent.TranscriptEventMessage || got[2].Role != "assistant" || got[2].Text != "done" {
		t.Fatalf("event[2] = %+v, want assistant MESSAGE \"done\"", got[2])
	}
}

// The result envelope is reconstructed from agent_message text — a fenced
// block split across the JSONL as one agent_message value must survive.
func TestStreamParser_ResultReconstructsEnvelope(t *testing.T) {
	p := newStreamParser()
	feed(p, []string{
		`{"type":"item.completed","item":{"id":"a","type":"agent_message","text":"here is the result:"}}`,
		"{\"type\":\"item.completed\",\"item\":{\"id\":\"b\",\"type\":\"agent_message\",\"text\":\"```json\\n{\\\"status\\\":\\\"IMPLEMENTED\\\",\\\"summary\\\":\\\"ok\\\"}\\n```\"}}",
	})
	got := p.Result()
	if !containsAll(got, `"status":"IMPLEMENTED"`, "```json") {
		t.Fatalf("Result() = %q, want the reconstructed fenced envelope", got)
	}
}

// A non-zero command exit is annotated so a failed command is visible in the
// transcript.
func TestStreamParser_CommandFailureAnnotatesExit(t *testing.T) {
	p := newStreamParser()
	got := feed(p, []string{
		`{"type":"item.completed","item":{"id":"x","type":"command_execution","command":"false","aggregated_output":"boom\n","exit_code":3,"status":"completed"}}`,
	})
	if len(got) != 1 || got[0].Type != agent.TranscriptEventToolResult {
		t.Fatalf("want one TOOL_RESULT, got %+v", got)
	}
	if !containsAll(got[0].ToolOutput, "boom", "exit code: 3") {
		t.Fatalf("tool output = %q, want it to note exit code 3", got[0].ToolOutput)
	}
}

// An unrecognized item type is preserved verbatim rather than dropped.
func TestStreamParser_UnknownItemPreserved(t *testing.T) {
	p := newStreamParser()
	raw := `{"type":"item.completed","item":{"id":"m","type":"mcp_tool_call","status":"completed"}}`
	got := feed(p, []string{raw})
	if len(got) != 1 || got[0].Type != agent.TranscriptEventToolResult || got[0].ToolName != "mcp_tool_call" {
		t.Fatalf("want a preserved mcp_tool_call TOOL_RESULT, got %+v", got)
	}
	if got[0].ToolOutput != raw {
		t.Fatalf("unknown item not preserved verbatim: %q", got[0].ToolOutput)
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

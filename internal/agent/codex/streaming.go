package codex

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
)

// streamParser turns Codex's `exec --json` JSONL event stream into
// agent.TranscriptEvents incrementally, so ExecuteCLI persists a transcript
// as each turn occurs (issue #257) instead of one coarse blob after the run.
// It mirrors internal/agent/claude's streaming parser, specialized to
// Codex's event vocabulary:
//
//	{"type":"thread.started","thread_id":"…"}
//	{"type":"turn.started"}
//	{"type":"item.started","item":{"id":"item_0","type":"command_execution","command":"…","status":"in_progress"}}
//	{"type":"item.completed","item":{"id":"item_0","type":"command_execution","aggregated_output":"…","exit_code":0,"status":"completed"}}
//	{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"…"}}
//	{"type":"turn.completed","usage":{…}}
//
// Codex also prints a couple of non-JSON banner lines ("Reading prompt from
// stdin...", "Shell cwd was reset to …"); those are ignored. The result
// envelope Forge instructs the backend to emit (a fenced ```json block, see
// clicommon.ResultContract) arrives as the final agent_message, so Result
// returns the concatenated agent_message text for ParseStructuredResult.
type streamParser struct {
	// message accumulates agent_message text (the envelope lives in the
	// final one); reasoning and tool events are streamed but not folded in.
	message strings.Builder
}

// newStreamParser returns a clicommon.StreamParser for one Codex invocation.
func newStreamParser() *streamParser { return &streamParser{} }

// codexEvent is the outer envelope of one Codex JSONL line. Only the fields
// Forge maps to transcript events are decoded; everything else is ignored so
// a Codex version that adds fields does not break parsing.
type codexEvent struct {
	Type string     `json:"type"`
	Item *codexItem `json:"item"`
}

// codexItem is the "item" payload carried by item.started / item.completed
// events. Type selects which fields are meaningful (text for agent_message
// and reasoning; command/aggregated_output/exit_code for command_execution).
type codexItem struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	Text             string `json:"text"`
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregated_output"`
	ExitCode         *int   `json:"exit_code"`
	Status           string `json:"status"`
}

// Line implements clicommon.StreamParser. A non-JSON or unrecognized line
// yields no events (Codex banner text, thread/turn lifecycle markers).
func (p *streamParser) Line(line string) []agent.TranscriptEvent {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return nil
	}
	var ev codexEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return nil
	}

	switch ev.Type {
	case "item.started":
		if ev.Item != nil && ev.Item.Type == "command_execution" {
			return []agent.TranscriptEvent{{
				Type:       agent.TranscriptEventToolCall,
				Role:       "assistant",
				ToolName:   "shell",
				ToolCallID: ev.Item.ID,
				ToolInput:  ev.Item.Command,
			}}
		}
		return nil
	case "item.completed":
		if ev.Item == nil {
			return nil
		}
		return p.itemCompleted(ev.Item, line)
	default:
		// thread.started, turn.started, turn.completed: lifecycle markers
		// with no transcript content.
		return nil
	}
}

// itemCompleted maps a completed item to its transcript event. agent_message
// is folded into the reconstructed result; command_execution becomes a tool
// result paired to its earlier tool call; every other item type is preserved
// verbatim as a tool result so nothing the Agent did is silently dropped.
func (p *streamParser) itemCompleted(item *codexItem, raw string) []agent.TranscriptEvent {
	switch item.Type {
	case "agent_message":
		if p.message.Len() > 0 {
			p.message.WriteString("\n")
		}
		p.message.WriteString(item.Text)
		return []agent.TranscriptEvent{{
			Type: agent.TranscriptEventMessage,
			Role: "assistant",
			Text: item.Text,
		}}
	case "reasoning":
		return []agent.TranscriptEvent{{
			Type: agent.TranscriptEventMessage,
			Role: "assistant",
			Text: item.Text,
		}}
	case "command_execution":
		return []agent.TranscriptEvent{{
			Type:       agent.TranscriptEventToolResult,
			Role:       "user",
			ToolName:   "shell",
			ToolCallID: item.ID,
			ToolOutput: commandResultText(item),
		}}
	default:
		return []agent.TranscriptEvent{{
			Type:       agent.TranscriptEventToolResult,
			Role:       "user",
			ToolName:   item.Type,
			ToolCallID: item.ID,
			ToolOutput: raw,
		}}
	}
}

// commandResultText renders a command_execution item's outcome: its captured
// output, annotated with the exit code when the command failed.
func commandResultText(item *codexItem) string {
	out := item.AggregatedOutput
	if item.ExitCode != nil && *item.ExitCode != 0 {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += "exit code: " + strconv.Itoa(*item.ExitCode)
	}
	return out
}

// Result implements clicommon.StreamParser: the concatenated agent_message
// text ExecuteCLI scans for the fenced result envelope.
func (p *streamParser) Result() string { return p.message.String() }

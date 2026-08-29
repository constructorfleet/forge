package agent

import (
	"sync"
	"time"
)

// TranscriptEventType enumerates the kinds of events a TranscriptSink can
// receive as an Agent invocation progresses (ticket 28's transcript
// logging: see CONTEXT.md "Agent").
type TranscriptEventType string

const (
	// TranscriptEventMessage is an assistant message turn.
	TranscriptEventMessage TranscriptEventType = "MESSAGE"
	// TranscriptEventToolCall is a tool invocation the Agent issued: its
	// name and bounded input.
	TranscriptEventToolCall TranscriptEventType = "TOOL_CALL"
	// TranscriptEventToolResult is the bounded result of a prior
	// TranscriptEventToolCall.
	TranscriptEventToolResult TranscriptEventType = "TOOL_RESULT"
)

// TranscriptEvent is one observed step of an Agent's work on an Issue: a
// message it emitted, a tool it called, or a tool's result. Text/ToolInput/
// ToolOutput are bounded by the emitter (see internal/textcap) before
// reaching a TranscriptSink, so a sink never has to defend against
// unbounded memory use on its own.
//
// The shape is deliberately flat and JSON-friendly rather than nested by
// turn/session: a later ticket adds live tailing (a TUI consuming these as
// they're emitted), and a flat, ordered event is what that consumer needs
// to render a growing list without re-parsing structure on every update.
type TranscriptEvent struct {
	// Seq orders events within one Agent invocation. Assigned by the
	// TranscriptSink on Emit; callers need not set it.
	Seq int

	Type TranscriptEventType

	// Role is the speaker ("assistant" for TranscriptEventMessage and
	// TranscriptEventToolCall, "user" for TranscriptEventToolResult,
	// mirroring the backend's own message roles).
	Role string

	// Text is the message body, populated only for TranscriptEventMessage.
	Text string

	// ToolName identifies the tool for TranscriptEventToolCall/
	// TranscriptEventToolResult.
	ToolName string

	// ToolInput is the tool call's bounded input, populated only for
	// TranscriptEventToolCall.
	ToolInput string

	// ToolOutput is the tool result's bounded output, populated only for
	// TranscriptEventToolResult.
	ToolOutput string

	Timestamp time.Time
}

// TranscriptSink receives TranscriptEvents as an Agent invocation
// progresses. Capture is best-effort: an Agent Adapter must never let a
// TranscriptSink, or its own streaming-parse of the backend's output, fail
// or change the outcome of Execute (see CONTEXT.md "Agent", ticket 28).
// AgentRequest.Transcript is optional and nil by default — supplying one is
// how a caller (currently only internal/engine) opts into capture.
type TranscriptSink interface {
	Emit(event TranscriptEvent)
}

var _ TranscriptSink = (*TranscriptRecorder)(nil)

// TranscriptRecorder is an in-memory TranscriptSink that buffers every
// Emitted event for a caller to read back once an Agent invocation
// completes. internal/engine passes one in as AgentRequest.Transcript and,
// after Execute returns, persists TranscriptRecorder.Events() keyed to the
// AgentRun it just recorded. Safe for concurrent use, though today's Agent
// Adapters emit from a single goroutine.
type TranscriptRecorder struct {
	mu     sync.Mutex
	events []TranscriptEvent
}

// NewTranscriptRecorder returns an empty TranscriptRecorder.
func NewTranscriptRecorder() *TranscriptRecorder {
	return &TranscriptRecorder{}
}

// Emit implements TranscriptSink. It assigns event.Seq from arrival order,
// overwriting whatever the caller set, so events are always numbered
// consistently regardless of the emitter's own bookkeeping.
func (r *TranscriptRecorder) Emit(event TranscriptEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	event.Seq = len(r.events)
	r.events = append(r.events, event)
}

// Events returns every Emitted event so far, in arrival order. The
// returned slice is a fresh copy, safe to retain independent of further
// Emit calls.
func (r *TranscriptRecorder) Events() []TranscriptEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]TranscriptEvent, len(r.events))
	copy(out, r.events)
	return out
}

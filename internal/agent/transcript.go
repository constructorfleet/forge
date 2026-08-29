package agent

import (
	"fmt"
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
	// TranscriptEventTruncation marks that earlier events were dropped to
	// keep a run's transcript bounded (issue 36). It carries a human-readable
	// count in Text and always sorts first (Seq 0) so a reader sees, up
	// front, that what follows is the most-recent window rather than an
	// unlabelled sliver.
	TranscriptEventTruncation TranscriptEventType = "TRUNCATION"
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

	// ToolCallID pairs a TranscriptEventToolResult back to the
	// TranscriptEventToolCall that produced it (issue 36): it holds the
	// backend's tool-use id, set on both the call (its own id) and the
	// matching result (the id it references). Empty for message events.
	ToolCallID string

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
// Emitted event for a caller to read back, optionally as the run
// progresses: internal/engine passes one in as AgentRequest.Transcript and
// persists TranscriptRecorder.Events() incrementally, after every Emit
// (issue 36), rather than waiting for Execute to return — so a
// killed/timed-out run's transcript survives up to the moment of the kill.
// Safe for concurrent use, though today's Agent Adapters emit from a single
// goroutine.
type TranscriptRecorder struct {
	mu      sync.Mutex
	events  []TranscriptEvent
	now     func() time.Time
	max     int
	dropped int
}

// NewTranscriptRecorder returns an empty, unbounded TranscriptRecorder using
// time.Now as its clock.
func NewTranscriptRecorder() *TranscriptRecorder {
	return NewBoundedTranscriptRecorder(0, nil)
}

// NewBoundedTranscriptRecorder returns an empty TranscriptRecorder that
// retains at most max events (0 means unbounded): once max is exceeded, the
// oldest retained event is dropped to make room for the newest, and
// Events() prepends a single TranscriptEventTruncation marker reporting how
// many events were dropped (issue 36) — so a bounded transcript is always
// explicit about the drop rather than silently presenting an unlabelled
// sliver. now defaults to time.Now when nil; tests inject a fixed/stepped
// clock for deterministic assertions.
func NewBoundedTranscriptRecorder(max int, now func() time.Time) *TranscriptRecorder {
	if now == nil {
		now = time.Now
	}
	return &TranscriptRecorder{now: now, max: max}
}

// Emit implements TranscriptSink. It assigns event.Timestamp from the
// recorder's clock when the caller left it zero — an emitter with its own
// per-event clock (e.g. the Claude adapter's stream-json timestamps) is
// preserved as-is — and evicts the oldest event once max is exceeded.
// event.Seq is ignored here; Events() assigns Seq from final arrival order
// on every read, since eviction can shift what "first" means.
func (r *TranscriptRecorder) Emit(event TranscriptEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if event.Timestamp.IsZero() {
		event.Timestamp = r.now()
	}
	r.events = append(r.events, event)
	if r.max > 0 && len(r.events) > r.max {
		r.events = r.events[1:]
		r.dropped++
	}
}

// Events returns every retained event, in arrival order, with Seq
// (re)assigned 0..n-1 across this read. When events were dropped to stay
// within max, the first event (Seq 0) is a synthetic
// TranscriptEventTruncation marker reporting the drop count, and every
// retained event is shifted to make room for it. The returned slice is a
// fresh copy, safe to retain independent of further Emit calls.
func (r *TranscriptRecorder) Events() []TranscriptEvent {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]TranscriptEvent, 0, len(r.events)+1)
	seq := 0
	if r.dropped > 0 {
		out = append(out, TranscriptEvent{
			Seq:  seq,
			Type: TranscriptEventTruncation,
			Text: fmt.Sprintf("%d earlier event(s) dropped to keep this transcript bounded", r.dropped),
		})
		seq++
	}
	for _, event := range r.events {
		event.Seq = seq
		out = append(out, event)
		seq++
	}
	return out
}

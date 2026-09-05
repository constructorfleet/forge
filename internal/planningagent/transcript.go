package planningagent

import (
	"context"
	"sync"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/storage"
)

// TranscriptPhase is stamped onto every transcript_events row a planning
// invocation persists (issue #248), the planning-phase counterpart of the
// execution phases internal/engine records ("IMPLEMENTING", "REVIEWING").
// Planning has a single phase — every stage of `forge plan` — with
// transcript_events.subagent (the InvokeRequest.Key: "specification-review",
// "ticket-plan-generation", ...) naming the stage within it.
const TranscriptPhase = "planning"

// TranscriptBackendName is recorded as agent_runs.backend for planning
// invocations, distinguishing them from the coding-provider values
// internal/engine writes ("claude-code", "fake", ...) without having to
// join against planning_executions.
const TranscriptBackendName = "planning"

// maxTranscriptEvents bounds how many TranscriptEvents one planning
// invocation retains in memory, mirroring internal/engine's identical cap: a
// chatty agent must not force Forge to hold an unbounded transcript.
// Exceeding it evicts the oldest retained event, leaving a seq gap the
// reader sees; the SQL retention cap is enforced separately at the store.
const maxTranscriptEvents = 500

// defaultFlushInterval is how long a batched transcript flush waits for more
// emits, debouncing rapid events into a single write (issue 489), mirroring
// internal/engine's sink.
const defaultFlushInterval = 250 * time.Millisecond

// defaultFlushTimeout bounds one transcript store write.
const defaultFlushTimeout = 5 * time.Second

// TranscriptStore is the subset of storage.Store an AgentBackend needs to
// record a planning invocation's agent_runs row and its transcript,
// narrowed (rather than taking all of storage.Store) for testability and to
// keep the dependency honest about what planning actually writes.
type TranscriptStore interface {
	StartAgentRun(ctx context.Context, run storage.AgentRun) (int64, error)
	FinalizeAgentRun(ctx context.Context, agentRunID int64, run storage.AgentRun) error
	RecordTranscriptEvents(ctx context.Context, executionID, issueID string, agentRunID int64, events []storage.TranscriptEvent) error
}

// toStorageTranscriptEvents translates agent.TranscriptEvents (the
// Agent-facing capture type) into storage.TranscriptEvents (the persisted
// shape), the translation convention storage documents: it has no
// dependency on internal/agent, so callers convert between the two.
func toStorageTranscriptEvents(events []agent.TranscriptEvent, phase, subagent string) []storage.TranscriptEvent {
	out := make([]storage.TranscriptEvent, len(events))
	for i, event := range events {
		out[i] = storage.TranscriptEvent{
			Seq:        event.Seq,
			Type:       string(event.Type),
			Role:       event.Role,
			Text:       event.Text,
			ToolName:   event.ToolName,
			ToolInput:  event.ToolInput,
			ToolOutput: event.ToolOutput,
			ToolCallID: event.ToolCallID,
			OccurredAt: event.Timestamp,
			Phase:      phase,
			Subagent:   subagent,
		}
	}
	return out
}

// persistingTranscriptSink is an agent.TranscriptSink that flushes the
// transcript to storage incrementally, as each event is Emitted, rather than
// batching everything into one write after Execute returns: a killed or
// timed-out planning run's transcript then survives up to the event
// immediately before the kill. It wraps a bounded
// agent.TranscriptRecorder, so what lands in storage is always the same
// bounded, truncation-marked window the recorder would return on its own.
//
// This mirrors internal/engine's sink of the same name rather than sharing
// it: engine depends on the lower-level packages, not the reverse, so
// planning must not import it.
type persistingTranscriptSink struct {
	// ctx is cancel-immune (issue 454). Each flush adds its own timeout.
	// Best-effort capture must outlive the invocation it describes: an
	// invocation cancelled before it streams anything emits only its
	// diagnostic fallback, and database/sql rejects a write on an already
	// cancelled context. The flush timeout prevents a stuck store from
	// blocking the invocation forever.
	ctx                  context.Context
	store                TranscriptStore
	executionID, issueID string
	agentRunID           int64
	// phase and subagent are stamped onto every event this sink persists
	// (issue #219's per-row tagging): TranscriptPhase, and the
	// InvokeRequest.Key naming which planning stage produced the event.
	phase, subagent string
	recorder        *agent.TranscriptRecorder
	flushInterval   time.Duration

	mu             sync.Mutex
	lastFlushedSeq int
	timer          *time.Timer
	closed         bool
}

func newPersistingTranscriptSink(ctx context.Context, store TranscriptStore, executionID, issueID string, agentRunID int64, subagent string, now func() time.Time) *persistingTranscriptSink {
	return &persistingTranscriptSink{
		ctx:            context.WithoutCancel(ctx),
		store:          store,
		executionID:    executionID,
		issueID:        issueID,
		agentRunID:     agentRunID,
		phase:          TranscriptPhase,
		subagent:       subagent,
		recorder:       agent.NewBoundedTranscriptRecorder(maxTranscriptEvents, now),
		lastFlushedSeq: -1,
	}
}

// Emit implements agent.TranscriptSink. Persistence is best-effort, the
// same contract internal/engine documents: a storage failure here is a
// durability gap for this invocation's transcript, never a reason to fail
// the planning call in progress. Emit schedules a debounced batch flush on
// its own goroutine; Close guarantees the final tail is flushed
// synchronously.
func (s *persistingTranscriptSink) Emit(event agent.TranscriptEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.recorder.Emit(event)
	interval := s.flushInterval
	if interval <= 0 {
		interval = defaultFlushInterval
	}
	if s.timer == nil {
		s.timer = time.AfterFunc(interval, func() { s.flush() })
	} else {
		s.timer.Reset(interval)
	}
}

// flush persists all events the recorder retains above the high-water mark,
// appending only unflushed seqs. Best-effort: a storage error drops the batch
// without advancing the watermark, so a later flush retries it.
func (s *persistingTranscriptSink) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLocked()
}

func (s *persistingTranscriptSink) flushLocked() {
	events := s.recorder.Events()
	if len(events) == 0 {
		return
	}
	var unflushed []agent.TranscriptEvent
	highWater := s.lastFlushedSeq
	for _, event := range events {
		if event.Seq > s.lastFlushedSeq {
			unflushed = append(unflushed, event)
			if event.Seq > highWater {
				highWater = event.Seq
			}
		}
	}
	if len(unflushed) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, defaultFlushTimeout)
	defer cancel()
	if err := s.store.RecordTranscriptEvents(ctx, s.executionID, s.issueID, s.agentRunID, toStorageTranscriptEvents(unflushed, s.phase, s.subagent)); err != nil {
		return
	}
	s.lastFlushedSeq = highWater
}

// Close performs the run's final synchronous flush, so the tail survives a
// cancelled run. Barrier-synchronized with the debounced async flush: the
// monotonic watermark prevents either from double-appending a seq. Emit after
// Close is a no-op.
func (s *persistingTranscriptSink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.flushLocked()
}

var _ agent.TranscriptSink = (*persistingTranscriptSink)(nil)

// noopTranscriptSink discards every event. Invoke installs one when
// StartAgentRun fails, so req.Transcript is still a usable sink (a nil one
// would mean silently reverting to no capture mid-flight, and an Adapter is
// entitled to assume a non-nil sink stays usable) while the rest of the
// invocation proceeds untouched.
type noopTranscriptSink struct{}

func (noopTranscriptSink) Emit(agent.TranscriptEvent) {}

var _ agent.TranscriptSink = noopTranscriptSink{}

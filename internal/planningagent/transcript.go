package planningagent

import (
	"context"
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
// invocation retains and persists, mirroring internal/engine's identical
// cap: a chatty agent must not force Forge to hold or persist an unbounded
// transcript. Exceeding it evicts the oldest event and surfaces a single
// TRUNCATION marker, so a reader sees explicitly that the persisted window
// is the most recent N events.
const maxTranscriptEvents = 500

// TranscriptStore is the subset of storage.Store an AgentBackend needs to
// record a planning invocation's agent_runs row and its transcript,
// narrowed (rather than taking all of storage.Store) for testability and to
// keep the dependency honest about what planning actually writes.
type TranscriptStore interface {
	StartAgentRun(ctx context.Context, run storage.AgentRun) (int64, error)
	FinalizeAgentRun(ctx context.Context, agentRunID int64, run storage.AgentRun) error
	ReplaceTranscriptEvents(ctx context.Context, executionID, issueID string, agentRunID int64, events []storage.TranscriptEvent) error
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
	ctx                  context.Context
	store                TranscriptStore
	executionID, issueID string
	agentRunID           int64
	// phase and subagent are stamped onto every event this sink persists
	// (issue #219's per-row tagging): TranscriptPhase, and the
	// InvokeRequest.Key naming which planning stage produced the event.
	phase, subagent string
	recorder        *agent.TranscriptRecorder
}

func newPersistingTranscriptSink(ctx context.Context, store TranscriptStore, executionID, issueID string, agentRunID int64, subagent string, now func() time.Time) *persistingTranscriptSink {
	return &persistingTranscriptSink{
		ctx:         ctx,
		store:       store,
		executionID: executionID,
		issueID:     issueID,
		agentRunID:  agentRunID,
		phase:       TranscriptPhase,
		subagent:    subagent,
		recorder:    agent.NewBoundedTranscriptRecorder(maxTranscriptEvents, now),
	}
}

// Emit implements agent.TranscriptSink. Persistence is best-effort, the
// same contract internal/engine documents: a storage failure here is a
// durability gap for this invocation's transcript, never a reason to fail
// the planning call in progress.
func (s *persistingTranscriptSink) Emit(event agent.TranscriptEvent) {
	s.recorder.Emit(event)
	_ = s.store.ReplaceTranscriptEvents(s.ctx, s.executionID, s.issueID, s.agentRunID, toStorageTranscriptEvents(s.recorder.Events(), s.phase, s.subagent))
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

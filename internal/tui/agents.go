package tui

// agents.go derives the agents pane's rows from the store's per-run
// transcript summaries: the implementation Agent first, then one row per
// review subagent in first-seen order.

import (
	"strings"
	"time"

	"github.com/Teagan42/forge/internal/storage"
)

// implementationLabel names the agents pane's first row: the single
// implementation Agent, whose events carry no subagent.
const implementationLabel = "implementation"

// phaseReviewing is the phase the review subagents stamp on their events. It
// mirrors domain.StateReviewing as a plain string, so the TUI keeps the label
// rule local.
const phaseReviewing = "REVIEWING"

// AgentRow is one line of the agents pane: one agent that worked the selected
// Issue. Retries of the same agent merge into one row.
type AgentRow struct {
	// Label is the row's display name: "implementation" or "review: <axis>".
	Label string
	// Phase is the workflow phase stamped on the agent's events.
	Phase string
	// Subagent names the review axis; empty for the implementation Agent. The
	// transcript filter matches on it.
	Subagent string
	// Events counts the agent's recorded events across every attempt.
	Events int
	// Latest is the one-line summary of the agent's newest event.
	Latest string
	// LastAt is when the newest event occurred. Zero when none is recorded.
	LastAt time.Time
}

// DeriveAgents folds the store's per-run summaries into agent rows. The
// implementation row always comes first, present even before it records an
// event; every other subagent follows in first-seen order. Runs that share a
// subagent merge: their event counts add up and the newest event wins.
func DeriveAgents(agents []storage.TranscriptAgent) []AgentRow {
	rows := []AgentRow{{Label: implementationLabel}}
	index := map[string]int{"": 0}
	for _, a := range agents {
		i, ok := index[a.Subagent]
		if !ok {
			i = len(rows)
			index[a.Subagent] = i
			rows = append(rows, AgentRow{Label: agentLabel(a.Phase, a.Subagent), Subagent: a.Subagent})
		}
		row := &rows[i]
		if row.Phase == "" {
			row.Phase = a.Phase
		}
		row.Events += a.Events
		if !a.Last.OccurredAt.Before(row.LastAt) {
			row.LastAt = a.Last.OccurredAt
			row.Latest = SummarizeEvent(a.Last)
			row.Phase = a.Phase
		}
	}
	return rows
}

// agentLabel names one agent row. The implementation Agent keeps its fixed
// label; a review subagent reads "review: <axis>"; any other subagent reads
// its phase in lower case before its name.
func agentLabel(phase, subagent string) string {
	if subagent == "" {
		return implementationLabel
	}
	if phase == phaseReviewing {
		return "review: " + subagent
	}
	if phase == "" {
		return subagent
	}
	return strings.ToLower(phase) + ": " + subagent
}

// SummarizeEvent renders one stored event as the one line the execution list
// and the agents pane show as latest output. A tool call reads by name, a tool
// result by name and its first output line, a message by its first line, a
// thinking message and a truncation marker each by their pane glyph.
func SummarizeEvent(e storage.TranscriptEvent) string {
	switch e.Type {
	case eventToolCall:
		return "▸ " + e.ToolName
	case eventToolResult:
		out := firstLine(e.ToolOutput)
		if out == "" {
			return "└ " + e.ToolName
		}
		return "└ " + e.ToolName + ": " + out
	case eventTruncation:
		return "░ " + firstLine(e.Text)
	case eventMessage:
		if e.Role == roleThinking {
			return "∴ " + firstLine(e.Text)
		}
		return firstLine(e.Text)
	default:
		return firstLine(e.Text)
	}
}

// AgentCount returns how many agents have worked the row's Issue.
func (r WorkerRow) AgentCount() int { return len(r.Agents) }

// LatestOutput returns the newest one-line output across the row's agents, or
// an empty string when no agent has recorded an event.
func (r WorkerRow) LatestOutput() string {
	var latest AgentRow
	found := false
	for _, a := range r.Agents {
		if a.LastAt.IsZero() {
			continue
		}
		if !found || a.LastAt.After(latest.LastAt) {
			latest, found = a, true
		}
	}
	return latest.Latest
}

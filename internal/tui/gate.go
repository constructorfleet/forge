package tui

// gate.go: quality-gate runs as synthetic transcript rows. A gate run is not
// an Agent transcript event and storage never persists one as such, so the
// pane derives the row from the store's GateRun records and interleaves it
// into the event timeline by finish time. No new TranscriptEvent type exists
// for a gate.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Teagan42/forge/internal/storage"
)

// maxGateOutputLines bounds a gate row's expanded output. A gate can emit a
// whole test log, which must never fill the pane.
const maxGateOutputLines = 40

// GateRow is one quality-gate run as the pane shows it. It holds the fields a
// row renders and nothing else, so the TUI keeps no dependency on
// internal/gate. Build a row with ConvertGateRun or ConvertGateRuns, which
// bound Output; a hand-built row renders a longer Output whole.
type GateRow struct {
	Name     string
	Command  string
	ExitCode int
	Passed   bool
	// Output is the run's combined stdout and stderr. ConvertGateRun keeps at
	// most maxGateOutputLines output lines, plus one marker line that counts the
	// dropped lines.
	Output string
	// FinishedAt is the run's finish time. It orders the rows and feeds the
	// cross-poll key, so a retry of one gate keeps its own identity.
	FinishedAt time.Time
	// AgentRunID is the AgentRun this gate row belongs to, so
	// currentAttemptGateRuns can scope rows to the current attempt by id.
	// Nil for a row recorded outside any AgentRun, or for one persisted
	// before gate_runs carried this column.
	AgentRunID *int64
	// key identifies the row across polls, so a pinned selection holds it.
	// gateEntries is the one place that derives it (see gateEntries).
	key string
}

// ConvertGateRun narrows a stored gate run to the fields a row renders and
// bounds its output.
func ConvertGateRun(run storage.GateRun) GateRow {
	return GateRow{
		Name:       run.Name,
		Command:    run.Command,
		ExitCode:   run.ExitCode,
		Passed:     run.Passed,
		Output:     boundGateOutput(run.Stdout, run.Stderr),
		FinishedAt: run.FinishedAt,
		AgentRunID: run.AgentRunID,
	}
}

// ConvertGateRuns converts every run and orders the rows oldest first, so the
// timeline reads in the order the gates ran.
func ConvertGateRuns(runs []storage.GateRun) []GateRow {
	ordered := append([]storage.GateRun(nil), runs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].FinishedAt.Before(ordered[j].FinishedAt)
	})
	rows := make([]GateRow, 0, len(ordered))
	for _, r := range ordered {
		rows = append(rows, ConvertGateRun(r))
	}
	return rows
}

// currentAttemptGateRuns keeps only the gate rows that belong to the Issue's
// latest recorded AgentRun, dropping rows from an earlier, reimplemented
// attempt. A gate row recorded with an AgentRunID (gate_runs.agent_run_id)
// is kept only if it names the latest AgentRun's id — a direct join, not a
// heuristic. A gate row with no AgentRunID (recorded before that column
// existed, or outside any AgentRun) falls back to the pre-existing
// time-window heuristic: a gate that finished before the latest AgentRun
// started belongs to an earlier attempt. With no recorded AgentRun yet (a
// planning or gate-only Issue), every row is kept, because there is no
// later attempt to have superseded it.
func currentAttemptGateRuns(runs []storage.AgentRun, gates []GateRow) []GateRow {
	if len(runs) == 0 {
		return gates
	}
	latest := runs[len(runs)-1]
	kept := make([]GateRow, 0, len(gates))
	for _, g := range gates {
		if g.AgentRunID != nil {
			if *g.AgentRunID == latest.ID {
				kept = append(kept, g)
			}
			continue
		}
		if !g.FinishedAt.Before(latest.StartedAt) {
			kept = append(kept, g)
		}
	}
	return kept
}

// boundGateOutput joins stdout and stderr and keeps at most
// maxGateOutputLines output lines plus one marker line. It keeps the tail,
// because gate tooling prints its verdict last.
func boundGateOutput(stdout, stderr string) string {
	parts := make([]string, 0, 2)
	for _, s := range []string{stdout, stderr} {
		if s = strings.TrimRight(s, "\n"); s != "" {
			parts = append(parts, s)
		}
	}
	joined := expandTabs(strings.Join(parts, "\n"))
	if joined == "" {
		return ""
	}
	lines := strings.Split(joined, "\n")
	if len(lines) <= maxGateOutputLines {
		return joined
	}
	dropped := len(lines) - maxGateOutputLines
	kept := []string{fmt.Sprintf("… %d earlier lines not shown", dropped)}
	kept = append(kept, lines[dropped:]...)
	return strings.Join(kept, "\n")
}

// expandTabs replaces every tab with a single space. A gate tool such as `go
// test` tab-separates its output columns, and the pane's cell-grid renderer
// draws a bare tab with no width, so the columns run together with no visible
// space. A tab becomes one space instead, which the renderer always draws.
func expandTabs(text string) string {
	return strings.ReplaceAll(text, "\t", " ")
}

// gateEntries turns gate rows into synthetic entries and derives each row's
// cross-poll key. The key holds the row's position, because retries record
// several runs of one gate name that can share a finish time. ConvertGateRuns
// sorts the rows by finish time with a stable sort, so the position holds across
// polls.
func gateEntries(rows []GateRow) []TranscriptEntry {
	entries := make([]TranscriptEntry, 0, len(rows))
	for i := range rows {
		g := rows[i]
		g.key = fmt.Sprintf("gate:%d:%s@%d", i, g.Name, g.FinishedAt.UnixNano())
		entries = append(entries, TranscriptEntry{Gate: &g})
	}
	return entries
}

// gateOutcome labels a gate row's result. A failed gate carries its exit code,
// because the code is what an operator acts on.
func gateOutcome(g GateRow) string {
	if g.Passed {
		return "pass"
	}
	return fmt.Sprintf("fail, exit %d", g.ExitCode)
}

// gateLines renders one gate row, collapsed to its name and outcome or
// expanded to its command, exit code, and bounded output. The collapsed preview
// keeps the last output line, which holds a gate tool's verdict. style colours
// the header green for a pass and red for a fail.
func gateLines(g GateRow, cur string, expanded bool, style Style) []string {
	headStyle := style.GatePass
	if !g.Passed {
		headStyle = style.GateFail
	}
	head := header(headerParts{cursor: cur, glyph: gateGlyph(g), text: fmt.Sprintf("gate %s (%s)", g.Name, gateOutcome(g))}, headStyle, style.Axis)
	if !expanded {
		if last := lastLine(g.Output); last != "" {
			return []string{head, indented(last)}
		}
		return []string{head}
	}
	// The header already carries the exit code of a failed gate, so only a pass
	// needs the code spelled out here.
	lines := []string{head, indented("$ " + g.Command)}
	if g.Passed {
		lines = append(lines, indented(fmt.Sprintf("exit %d", g.ExitCode)))
	}
	return append(lines, indentedBlock(g.Output)...)
}

// gateGlyph marks a gate row's column with its outcome. The glyphs differ from
// the tool-call arrows, because a gate is the orchestrator's own work and not
// the Agent's.
func gateGlyph(g GateRow) string {
	if g.Passed {
		return "✓"
	}
	return "✗"
}

// gateDetailLine renders the selected gate row's name, command, exit code, and
// outcome.
func gateDetailLine(g GateRow) string {
	cmd := g.Command
	if cmd == "" {
		cmd = "—"
	}
	return fmt.Sprintf("gate %s | %s | exit %d | %s", g.Name, cmd, g.ExitCode, gateOutcome(g))
}

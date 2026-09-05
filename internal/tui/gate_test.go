package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/storage"
)

func gateView(events ...TranscriptEvent) TranscriptViewModel {
	return TranscriptViewModel{Events: events, AtTail: true, Retained: len(events)}
}

func TestConvertGateRunJoinsBoundedOutput(t *testing.T) {
	run := storage.GateRun{
		Name:     "test",
		Command:  "go test ./...",
		ExitCode: 1,
		Stdout:   "ok  pkg/a\nFAIL pkg/b\n",
		Stderr:   "exit status 1\n",
	}
	row := ConvertGateRun(run)
	if row.Name != "test" || row.Command != "go test ./..." || row.ExitCode != 1 || row.Passed {
		t.Fatalf("unexpected row: %+v", row)
	}
	want := "ok  pkg/a\nFAIL pkg/b\nexit status 1"
	if row.Output != want {
		t.Fatalf("output = %q, want %q", row.Output, want)
	}
}

// TestConvertGateRunExpandsTabsToSpaces proves a raw `go test` line, which
// tab-separates the package path from the run time, renders with a visible
// space between the two: the pane's renderer treats a bare tab as zero
// width, so a run time such as "7.376s" would otherwise sit flush against
// the package path.
func TestConvertGateRunExpandsTabsToSpaces(t *testing.T) {
	run := storage.GateRun{
		Name:   "test",
		Stdout: "ok  \tgithub.com/Teagan42/forge/internal/workspace\t7.376s\n",
	}
	row := ConvertGateRun(run)
	if strings.Contains(row.Output, "\t") {
		t.Fatalf("output keeps a raw tab: %q", row.Output)
	}
	want := "ok   github.com/Teagan42/forge/internal/workspace 7.376s"
	if row.Output != want {
		t.Fatalf("output = %q, want %q", row.Output, want)
	}
}

func TestConvertGateRunBoundsOutputLines(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxGateOutputLines+10; i++ {
		b.WriteString("line\n")
	}
	row := ConvertGateRun(storage.GateRun{Name: "lint", Stdout: b.String()})
	lines := strings.Split(row.Output, "\n")
	if len(lines) != maxGateOutputLines+1 {
		t.Fatalf("got %d lines, want %d", len(lines), maxGateOutputLines+1)
	}
	if !strings.Contains(lines[0], "10 earlier lines") {
		t.Fatalf("missing bound marker: %q", lines[0])
	}
}

func TestConvertGateRunKeepsTheOutputTail(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxGateOutputLines; i++ {
		b.WriteString("head\n")
	}
	row := ConvertGateRun(storage.GateRun{Name: "test", Stdout: b.String(), Stderr: "exit status 1\n"})
	if !strings.Contains(row.Output, "exit status 1") {
		t.Fatalf("bounded output dropped the failure tail:\n%s", row.Output)
	}
}

func TestGateRowsRenderAsSyntheticEntries(t *testing.T) {
	p := NewTranscriptPane()
	p.SetGates([]GateRow{{Name: "test", Command: "go test ./...", ExitCode: 0, Passed: true, Output: "ok\n"}})
	p.SetView(gateView(TranscriptEvent{Seq: 0, Type: eventMessage, Text: "hello"}))

	if got := len(p.Entries()); got != 2 {
		t.Fatalf("entries = %d, want 2", got)
	}
	last := p.Entries()[1]
	if !last.IsGate() {
		t.Fatal("last entry is not a gate row")
	}
	out := RenderTranscript(p)
	if !strings.Contains(out, "gate test (pass)") {
		t.Fatalf("collapsed gate row missing:\n%s", out)
	}
	if strings.Contains(out, "go test ./...") {
		t.Fatalf("collapsed gate row leaked its command:\n%s", out)
	}
}

func TestGateRowExpandsToCommandExitCodeAndOutput(t *testing.T) {
	p := NewTranscriptPane()
	p.SetGates([]GateRow{{Name: "lint", Command: "golangci-lint run", ExitCode: 2, Output: "a.go:1: bad\n"}})
	p.SetView(gateView(TranscriptEvent{Seq: 0, Type: eventMessage, Text: "hello"}))
	p.Select(1) // the gate row

	if !p.CanExpand() {
		t.Fatal("gate row is not expandable")
	}
	p.ToggleExpand()
	out := RenderTranscript(p)
	for _, want := range []string{"gate lint (fail, exit 2)", "$ golangci-lint run", "a.go:1: bad"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expanded gate row missing %q:\n%s", want, out)
		}
	}
	p.ToggleExpand()
	if strings.Contains(RenderTranscript(p), "golangci-lint run") {
		t.Fatal("toggle did not collapse the gate row")
	}
}

func TestGateRowsUseNoNewEventTypes(t *testing.T) {
	p := NewTranscriptPane()
	p.SetGates([]GateRow{{Name: "vet"}})
	p.SetView(gateView())
	e := p.Entries()[0]
	if e.Event.Type != "" || e.Event.Seq != 0 {
		t.Fatalf("gate entry carries a synthetic event: %+v", e.Event)
	}
}

func TestGateRowsSortByFinishTime(t *testing.T) {
	base := time.Unix(1000, 0)
	rows := ConvertGateRuns([]storage.GateRun{
		{Name: "late", FinishedAt: base.Add(2 * time.Second)},
		{Name: "early", FinishedAt: base},
	})
	if rows[0].Name != "early" || rows[1].Name != "late" {
		t.Fatalf("unsorted rows: %+v", rows)
	}
}

func TestGateSelectionSurvivesPoll(t *testing.T) {
	p := NewTranscriptPane()
	p.SetGates([]GateRow{{Name: "test", Passed: true}, {Name: "lint"}})
	p.SetView(gateView(TranscriptEvent{Seq: 0, Type: eventMessage, Text: "a"}))

	p.Select(1) // the "test" gate row
	p.ToggleExpand()
	p.SetView(gateView(
		TranscriptEvent{Seq: 0, Type: eventMessage, Text: "a"},
		TranscriptEvent{Seq: 1, Type: eventMessage, Text: "b"},
	))
	e, ok := p.SelectedEntry()
	if !ok || !e.IsGate() || e.Gate.Name != "test" {
		t.Fatalf("selection moved off the gate row: %+v", e)
	}
	if !p.Expanded(p.selection) {
		t.Fatal("expansion did not survive the poll")
	}
}

func TestGateDetailLine(t *testing.T) {
	e := TranscriptEntry{Gate: &GateRow{Name: "test", Command: "go test ./...", ExitCode: 1}}
	got := transcriptDetailLine(e)
	if !strings.Contains(got, "gate test") || !strings.Contains(got, "exit 1") || !strings.Contains(got, "fail") {
		t.Fatalf("detail line = %q", got)
	}
}

func TestCollapsedGateRowPreviewsTheVerdictLine(t *testing.T) {
	p := NewTranscriptPane()
	p.SetGates([]GateRow{{Name: "test", Output: "ok  pkg/a\nFAIL pkg/b\nexit status 1"}})
	out := RenderTranscript(p)
	if !strings.Contains(out, "exit status 1") {
		t.Fatalf("collapsed gate row hides the verdict:\n%s", out)
	}
}

func TestGateGlyphCarriesTheOutcome(t *testing.T) {
	p := NewTranscriptPane()
	p.SetGates([]GateRow{{Name: "lint", ExitCode: 2}})
	if out := RenderTranscript(p); !strings.Contains(out, "✗ gate lint") {
		t.Fatalf("failed gate lacks the fail glyph:\n%s", out)
	}
	p.SetGates([]GateRow{{Name: "lint", Passed: true}})
	if out := RenderTranscript(p); !strings.Contains(out, "✓ gate lint") {
		t.Fatalf("passed gate lacks the pass glyph:\n%s", out)
	}
}

func TestUnpinnedSelectionFollowsTheNewestEventPastGateRows(t *testing.T) {
	p := NewTranscriptPane()
	p.SetGates([]GateRow{{Name: "test", Passed: true}})
	p.SetView(gateView(
		TranscriptEvent{Seq: 0, Type: eventMessage, Text: "a"},
		TranscriptEvent{Seq: 1, Type: eventMessage, Text: "b"},
	))
	e, ok := p.SelectedEntry()
	if !ok || e.IsGate() || e.Event.Seq != 1 {
		t.Fatalf("tail selection stuck on a gate row: %+v", e)
	}
}

func TestSetGatesClearsExpansionWhenTheRowsChange(t *testing.T) {
	p := NewTranscriptPane()
	p.SetGates([]GateRow{{Name: "test", Passed: true}, {Name: "lint"}})
	p.Select(1) // the "lint" gate row
	p.ToggleExpand()

	p.SetGates([]GateRow{{Name: "vet"}})
	if p.Expanded(p.selection) {
		t.Fatal("expansion survived onto a different gate row")
	}
}

func TestGateEntriesKeyEveryRow(t *testing.T) {
	entries := gateEntries([]GateRow{{Name: "test"}, {Name: "lint"}})
	for i, e := range entries {
		if e.key() == "" {
			t.Fatalf("entry %d has no key", i)
		}
	}
	if a, b := entries[0].key(), entries[1].key(); a == b {
		t.Fatalf("gate rows share the key %q", a)
	}
}

// gateScroller records the window moves the pane asks for.
type gateScroller struct {
	up, down, tails int
}

func (s *gateScroller) ScrollUp(n int)   { s.up += n }
func (s *gateScroller) ScrollDown(n int) { s.down += n }
func (s *gateScroller) ScrollToTail()    { s.tails++ }

func TestDownAtTheLastEventScrollsPastTrailingGateRows(t *testing.T) {
	p := NewTranscriptPane()
	sc := &gateScroller{}
	p.SetScroller(sc)
	p.SetGates(ConvertGateRuns([]storage.GateRun{{Name: "test", FinishedAt: time.Unix(1000, 0)}}))
	p.SetView(TranscriptViewModel{
		Events:   []TranscriptEvent{{Seq: 0, Type: eventMessage, Text: "a"}},
		Retained: 1,
	})

	p.Select(0)
	p.MoveSelection(1)
	if sc.down != 1 {
		t.Fatalf("ScrollDown events = %d, want 1: the gate row swallowed the tail edge", sc.down)
	}
}

func TestGateKeysDifferForRunsThatShareAFinishTime(t *testing.T) {
	at := time.Unix(1000, 0)
	rows := ConvertGateRuns([]storage.GateRun{{Name: "test", FinishedAt: at}, {Name: "test", FinishedAt: at}})
	entries := gateEntries(rows)
	if a, b := entries[0].key(), entries[1].key(); a == b {
		t.Fatalf("two runs of one gate share the key %q", a)
	}
}

func TestSetGatesCopiesTheCallersRows(t *testing.T) {
	p := NewTranscriptPane()
	rows := []GateRow{{Name: "test", Passed: true}}
	p.SetGates(rows)
	rows[0].Name = "mutated"
	if out := RenderTranscript(p); !strings.Contains(out, "gate test") {
		t.Fatalf("the pane shares the caller's slice:\n%s", out)
	}
}

func TestSetGatesSelectsARowOnAnEmptyPane(t *testing.T) {
	p := NewTranscriptPane()
	p.SetGates([]GateRow{{Name: "vet", Passed: true}})
	e, ok := p.SelectedEntry()
	if !ok || !e.IsGate() {
		t.Fatalf("gate row is not selectable: %+v", e)
	}
}

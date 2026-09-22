package tui_test

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tui"
)

func TestAttentionGlyph(t *testing.T) {
	cases := []struct {
		name string
		att  tui.Attention
		want string
	}{
		{"needs answer", tui.AttentionNeedsAnswer, "!"},
		{"running tool", tui.AttentionRunningTool, "*"},
		{"none", tui.AttentionNone, " "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tui.AttentionGlyph(tc.att); got != tc.want {
				t.Fatalf("AttentionGlyph(%v) = %q, want %q", tc.att, got, tc.want)
			}
		})
	}
}

func TestLivenessGlyph(t *testing.T) {
	cases := []struct {
		name string
		live tui.Liveness
		want string
	}{
		{"live", tui.LivenessLive, "\u2022"},
		{"stale", tui.LivenessStale, "\u00d7"},
		{"none", tui.LivenessNone, " "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tui.LivenessGlyph(tc.live); got != tc.want {
				t.Fatalf("LivenessGlyph(%v) = %q, want %q", tc.live, got, tc.want)
			}
		})
	}
}

func TestDeriveAttention(t *testing.T) {
	cases := []struct {
		name  string
		state domain.IssueState
		tool  string
		want  tui.Attention
	}{
		// GroupBlocked states await human input or a backoff; flag for attention.
		{"needs info", domain.StateNeedsInfo, "", tui.AttentionNeedsAnswer},
		{"needs replan", domain.StateNeedsReplan, "", tui.AttentionNeedsAnswer},
		{"provider limit", domain.StateProviderLimit, "", tui.AttentionNeedsAnswer},
		// A tool in flight marks a running Worker.
		{"tool running", domain.StateImplementing, "git status", tui.AttentionRunningTool},
		// Everything else is quiet.
		{"working idle", domain.StateImplementing, "", tui.AttentionNone},
		{"pending", domain.StatePending, "", tui.AttentionNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tui.DeriveAttention(tc.state, tc.tool); got != tc.want {
				t.Fatalf("DeriveAttention(%s, %q) = %v, want %v", tc.state, tc.tool, got, tc.want)
			}
		})
	}
}

func TestDeriveLiveness(t *testing.T) {
	within := 3 * time.Second
	over := 30 * time.Second
	cases := []struct {
		name    string
		hasBeat bool
		age     time.Duration
		want    tui.Liveness
	}{
		{"live within window", true, within, tui.LivenessLive},
		{"stale over window", true, over, tui.LivenessStale},
		{"no heartbeat", false, 0, tui.LivenessNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tui.DeriveLiveness(tc.hasBeat, tc.age); got != tc.want {
				t.Fatalf("DeriveLiveness(%v, %s) = %v, want %v", tc.hasBeat, tc.age, got, tc.want)
			}
		})
	}
}

func TestDeriveTranscriptLag(t *testing.T) {
	poll := time.Second
	cases := []struct {
		name string
		age  time.Duration
		poll time.Duration
		want bool
	}{
		{"within threshold", 2 * time.Second, poll, false},
		{"at threshold", 3 * time.Second, poll, false},
		{"over threshold", 4 * time.Second, poll, true},
		{"zero poll never lags", 100 * time.Second, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tui.DeriveTranscriptLag(tc.age, tc.poll); got != tc.want {
				t.Fatalf("DeriveTranscriptLag(%s, %s) = %v, want %v", tc.age, tc.poll, got, tc.want)
			}
		})
	}
}

func TestLegalKeys(t *testing.T) {
	cases := []struct {
		state domain.IssueState
		want  []string
	}{
		// q (quit) always legal; c (cancel) legal from any non-terminal state.
		{domain.StatePending, []string{"[q] quit", "[c] cancel"}},
		{domain.StateImplementing, []string{"[q] quit", "[c] cancel"}},
		// r (retry) is the manual retry out of the terminal FAILED state.
		{domain.StateFailed, []string{"[q] quit", "[r] retry"}},
		// a (answer) is legal only while parked on a NEEDS_INFO decision.
		{domain.StateNeedsInfo, []string{"[q] quit", "[c] cancel", "[a] answer"}},
		// p (approve) is legal only while parked on NEEDS_REPLAN.
		{domain.StateNeedsReplan, []string{"[q] quit", "[c] cancel", "[p] approve"}},
		// Terminal states carry only q: no further transitions.
		{domain.StateDone, []string{"[q] quit"}},
		{domain.StateCancelled, []string{"[q] quit"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			got := tui.LegalKeys(tc.state)
			if len(got) != len(tc.want) {
				t.Fatalf("LegalKeys(%s) = %v keys, want %v", tc.state, got, tc.want)
			}
			for i := range tc.want {
				if got[i].String() != tc.want[i] {
					t.Fatalf("LegalKeys(%s)[%d] = %q, want %q", tc.state, i, got[i].String(), tc.want[i])
				}
			}
		})
	}
}

// layoutFixture builds a frame with two Workers, three agents on the selected
// one, a short transcript, and an open diff, so the layout tests can prove
// every region renders from the one view-model.
func layoutFixture() tui.ViewModel {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{AtTail: true, RunOrder: []int64{7}, Events: []tui.TranscriptEvent{
		{AgentRunID: 7, Seq: 0, Type: "MESSAGE", Text: "starting work"},
		{AgentRunID: 7, Seq: 1, Type: "TOOL_CALL", ToolName: "bash", ToolInput: "go build ./...", ToolCallID: "t1"},
	}})
	at := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	return tui.ViewModel{
		ExecutionID: "45c7c799-981e-4852-803b-689cf48dd900",
		Selection:   1,
		Workers: []tui.WorkerRow{
			{IssueID: "#1", Title: "Write tests", State: domain.StatePending, Agents: tui.DeriveAgents(nil)},
			{
				IssueID: "#2", Title: "Add roster frame", State: domain.StateReviewing,
				Elapsed: 62 * time.Second, HasHeartbeat: true, HeartbeatAge: 3 * time.Second,
				Attempt: 2, Budget: 3, Tool: "git status", HasDiff: true, Verdict: "PASS",
				ProgressDone: 4, ProgressTotal: 5,
				Agents: []tui.AgentRow{
					{Label: "implementation", Events: 12, Latest: "Fixed the finding.", LastAt: at},
					{Label: "review: bugs", Subagent: "bugs", Events: 7, Latest: "▸ Grep", LastAt: at.Add(time.Second)},
					{Label: "review: docs", Subagent: "docs", Events: 1, Latest: "Docs ok.", LastAt: at.Add(-time.Second)},
				},
			},
		},
		AgentSelection: 1,
		Transcript:     pane,
		DiffOpen:       true,
		Diff: &tui.DiffSummary{
			Files:     []tui.DiffFile{{Path: "internal/tui/frame.go", Additions: 12, Deletions: 3}, {Path: "docs/spec.md", Additions: 2}},
			Additions: 14, Deletions: 3,
		},
	}
}

// TestRenderDrawsEveryRegion proves the frame draws the execution list, the
// agents pane, the output pane, and the diff pane from one view-model, each
// under its own bordered header, with the selected Worker and agent marked.
func TestRenderDrawsEveryRegion(t *testing.T) {
	got := tui.Render(layoutFixture())
	for _, want := range []string{
		"EXECUTIONS",                                                             // region header names the execution list
		"ID", "NAME", "STATUS", "PROGRESS", "ELAPSED", "AGENTS", "LATEST OUTPUT", // list columns
		"#1", "Write tests", "PENDING",
		"> ", "#2", "Add roster frame", "REVIEWING", "4/5 (80%)", "1m2s", "▸ Grep", // the selected row and its newest output
		"SUBAGENTS", "  implementation", "> review: bugs", "  review: docs",
		"OUTPUT", "starting work", "▸ bash",
		"DIFF", "internal/tui/frame.go", "docs/spec.md",
		"REVIEWING | elapsed 1m2s | beat 3s | attempt 2/3 | tool git status | verdict PASS",
		"[q] quit",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("frame omits %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("a zero Style must render no ANSI escape:\n%s", got)
	}
	for _, line := range splitLines(got) {
		if strings.ContainsAny(line, "│║") && !strings.HasSuffix(line, "│") && !strings.HasSuffix(line, "║") {
			t.Errorf("bordered row does not close its border: %q", line)
		}
	}
}

// TestRenderAgentCountColumn proves the execution list counts every agent
// that worked the row, so the operator sees the review fan-out at a glance.
func TestRenderAgentCountColumn(t *testing.T) {
	vm := layoutFixture()
	vm.DiffOpen = false
	lines := splitLines(tui.Render(vm))
	var selected string
	for _, l := range lines {
		if strings.Contains(l, "#2") && strings.Contains(l, "Add roster frame") {
			selected = l
		}
	}
	if selected == "" {
		t.Fatalf("no execution row for #2:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(selected, " 3 ") {
		t.Errorf("row does not carry its agent count of 3: %q", selected)
	}
}

// TestRenderExecutionListShowsThreeRowsAndScrolls proves the list shows at
// most three Workers and scrolls to keep the selection visible, naming the
// visible range in its header.
func TestRenderExecutionListShowsThreeRowsAndScrolls(t *testing.T) {
	var rows []tui.WorkerRow
	for i := 1; i <= 5; i++ {
		rows = append(rows, tui.WorkerRow{IssueID: fmt.Sprintf("#%d", i), Title: fmt.Sprintf("issue %d", i), State: domain.StateImplementing})
	}
	vm := tui.ViewModel{Workers: rows, Selection: 4}
	got := tui.Render(vm)
	for _, want := range []string{"issue 3", "issue 4", "issue 5", "3-5 of 5"} {
		if !strings.Contains(got, want) {
			t.Errorf("scrolled list omits %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"issue 1", "issue 2"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("scrolled list still shows %q:\n%s", unwanted, got)
		}
	}
	vm.Selection = 0
	got = tui.Render(vm)
	if !strings.Contains(got, "issue 1") || strings.Contains(got, "issue 4") || !strings.Contains(got, "1-3 of 5") {
		t.Errorf("list at the top shows the wrong window:\n%s", got)
	}
	if got := tui.Render(tui.ViewModel{Workers: rows[:2]}); strings.Contains(got, " of 2") {
		t.Errorf("a list that fits still names a range:\n%s", got)
	}
}

// TestRenderDiffPaneClosedByDefault proves the diff pane draws only while
// open, and the footer offers the toggle only where the store holds a diff.
func TestRenderDiffPaneClosedByDefault(t *testing.T) {
	vm := layoutFixture()
	vm.DiffOpen = false
	got := tui.Render(vm)
	if strings.Contains(got, "internal/tui/frame.go") {
		t.Errorf("closed diff pane still draws its files:\n%s", got)
	}
	if !strings.Contains(got, "[d] diff") {
		t.Errorf("footer omits the diff toggle for a row with a diff:\n%s", got)
	}
	if got := tui.Render(layoutFixture()); !strings.Contains(got, "[d] hide diff") {
		t.Errorf("footer does not offer to hide the open diff:\n%s", got)
	}
	vm.Selection = 0
	if got := tui.Render(vm); strings.Contains(got, "[d]") {
		t.Errorf("footer offers the diff key for a row with no diff:\n%s", got)
	}
}

// TestRenderDiffPaneWithoutSummaryExplainsItself proves an open pane whose
// diff has not loaded yet says so rather than drawing an empty box.
func TestRenderDiffPaneWithoutSummaryExplainsItself(t *testing.T) {
	vm := layoutFixture()
	vm.Diff = nil
	if got := tui.Render(vm); !strings.Contains(got, "loading diff") {
		t.Errorf("open diff pane with no summary is blank:\n%s", got)
	}
	vm.Diff = &tui.DiffSummary{}
	if got := tui.Render(vm); !strings.Contains(got, "no changed files") {
		t.Errorf("open diff pane with an empty summary is blank:\n%s", got)
	}
}

// TestRenderFocusedPaneUsesDoubleBorder proves the focused region reads apart
// from the others without colour: it alone draws a double-line border.
func TestRenderFocusedPaneUsesDoubleBorder(t *testing.T) {
	for _, tc := range []struct {
		focus tui.Pane
		title string
	}{
		{tui.PaneRoster, "EXECUTIONS"},
		{tui.PaneAgents, "SUBAGENTS"},
		{tui.PaneTranscript, "OUTPUT"},
		{tui.PaneDiff, "DIFF"},
	} {
		vm := layoutFixture()
		vm.Focus = tc.focus
		lines := splitLines(tui.Render(vm))
		double, single := 0, 0
		for _, l := range lines {
			if strings.Contains(l, "╔") || strings.Contains(l, "╗") {
				double++
				if !strings.Contains(l, tc.title) {
					t.Errorf("focus %v: double border on a line without %q: %q", tc.focus, tc.title, l)
				}
			}
			if strings.Contains(l, "┌") {
				single++
			}
		}
		if double == 0 {
			t.Errorf("focus %v: no pane draws the double border:\n%s", tc.focus, strings.Join(lines, "\n"))
		}
		if single == 0 {
			t.Errorf("focus %v: every pane draws the double border:\n%s", tc.focus, strings.Join(lines, "\n"))
		}
	}
}

// TestRenderFooterPerPane proves each focused pane advertises its own keys:
// pane navigation, execution navigation, agent navigation, diff toggling, and
// inspection.
func TestRenderFooterPerPane(t *testing.T) {
	cases := []struct {
		focus tui.Pane
		want  []string
		omit  []string
	}{
		{tui.PaneRoster, []string{"[q] quit", "[c] cancel", "[j/k] switch worker", "[enter] inspect", "[tab] next pane", "[d] hide diff"}, []string{"switch agent"}},
		{tui.PaneAgents, []string{"[q] quit", "[j/k] switch agent", "[enter] inspect", "[tab] next pane", "[d] hide diff"}, []string{"[c] cancel", "switch worker"}},
		{tui.PaneTranscript, []string{"[q] quit", "[tab] next pane", "[enter] expand", "[d] hide diff"}, []string{"[c] cancel", "switch worker"}},
		{tui.PaneDiff, []string{"[q] quit", "[enter] open in $PAGER", "[d] hide diff", "[tab] next pane"}, []string{"[c] cancel", "expand"}},
	}
	for _, tc := range cases {
		vm := layoutFixture()
		vm.Focus = tc.focus
		lines := splitLines(tui.Render(vm))
		footer := lines[len(lines)-1]
		for _, w := range tc.want {
			if !strings.Contains(footer, w) {
				t.Errorf("focus %v: footer %q omits %q", tc.focus, footer, w)
			}
		}
		for _, o := range tc.omit {
			if strings.Contains(footer, o) {
				t.Errorf("focus %v: footer %q must not offer %q", tc.focus, footer, o)
			}
		}
	}
}

// TestRenderFooterLeadsWithLegalKeys proves the roster footer starts with
// exactly the keys legal for the selected row's state, derived from the same
// view-model as the row, so it can never advertise an illegal action.
func TestRenderFooterLeadsWithLegalKeys(t *testing.T) {
	states := []domain.IssueState{
		domain.StatePending, domain.StateImplementing, domain.StateFailed,
		domain.StateNeedsInfo, domain.StateNeedsReplan, domain.StateDone, domain.StateCancelled,
	}
	for _, s := range states {
		vm := tui.ViewModel{Workers: []tui.WorkerRow{{IssueID: "#1", Title: "t", State: s}}}
		lines := splitLines(tui.Render(vm))
		footer := lines[len(lines)-1]
		if want := footerFor(tui.LegalKeys(s)); !strings.HasPrefix(footer, want) {
			t.Errorf("state %s: footer %q does not lead with %q", s, footer, want)
		}
		for _, illegal := range []string{"[c] cancel", "[r] retry", "[a] answer", "[p] approve"} {
			if strings.Contains(footer, illegal) && !strings.Contains(footerFor(tui.LegalKeys(s)), illegal) {
				t.Errorf("state %s: footer %q advertises the illegal %q", s, footer, illegal)
			}
		}
	}
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func footerFor(keys []tui.KeyBinding) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k.String()
	}
	return strings.Join(parts, " ")
}

// TestRenderMarksTranscriptHeaderWhenLagging proves a transcript read older
// than transcriptLagMultiple poll intervals marks the frame, so a store slower
// than the poll cadence is visible rather than merely a thinned refresh rate.
func TestRenderMarksTranscriptHeaderWhenLagging(t *testing.T) {
	vm := tui.ViewModel{PollInterval: time.Second, TranscriptLagAge: 4 * time.Second}
	if got := tui.Render(vm); !strings.Contains(got, "lagging") {
		t.Fatalf("Render does not mark a lagging transcript:\n%s", got)
	}
	vm.TranscriptLagAge = time.Second
	if got := tui.Render(vm); strings.Contains(got, "lagging") {
		t.Fatalf("Render marks lag under the threshold:\n%s", got)
	}
}

// TestRenderEmptyRoster proves the first frame, before any row, draws the
// chrome with its notice and offers quit alone.
func TestRenderEmptyRoster(t *testing.T) {
	got := tui.Render(tui.ViewModel{Notice: "waiting"})
	if !strings.Contains(got, "waiting") || !strings.Contains(got, "EXECUTIONS") {
		t.Fatalf("empty frame omits the notice or the list header:\n%s", got)
	}
	lines := splitLines(got)
	if footer := lines[len(lines)-1]; footer != "[q] quit" {
		t.Fatalf("empty frame footer = %q, want %q", footer, "[q] quit")
	}
	if strings.Contains(got, "SUBAGENTS") {
		t.Fatalf("empty frame draws an agents pane with no Worker:\n%s", got)
	}
}

// TestRenderStripFollowsFocus proves the detail strip describes the focused
// pane's own selection: the Worker, the agent, the transcript entry, or the
// diff.
func TestRenderStripFollowsFocus(t *testing.T) {
	cases := []struct {
		focus tui.Pane
		want  string
	}{
		{tui.PaneRoster, "REVIEWING | elapsed 1m2s"},
		{tui.PaneAgents, "review: bugs | events 7 | last ▸ Grep"},
		{tui.PaneDiff, "diff #2 | 2 files | +14 -3"},
	}
	for _, tc := range cases {
		vm := layoutFixture()
		vm.Focus = tc.focus
		lines := splitLines(tui.Render(vm))
		if strip := lines[len(lines)-2]; !strings.Contains(strip, tc.want) {
			t.Errorf("focus %v: strip %q does not carry %q", tc.focus, strip, tc.want)
		}
	}
}

// TestRenderFillsTheTerminalHeight proves a sized frame draws exactly
// Height rows, with the newest transcript row kept and the footer last, so
// the frame never draws past the terminal bottom.
func TestRenderFillsTheTerminalHeight(t *testing.T) {
	for _, height := range []int{14, 20, 30} {
		vm := layoutFixture()
		vm.Height, vm.Width = height, 120
		lines := splitLines(tui.Render(vm))
		if len(lines) != height {
			t.Errorf("height %d: Render emitted %d rows:\n%s", height, len(lines), strings.Join(lines, "\n"))
		}
		if !strings.Contains(lines[len(lines)-1], "[q] quit") {
			t.Errorf("height %d: footer is not the last row: %q", height, lines[len(lines)-1])
		}
		if want := tui.TranscriptRows(vm); want < 1 {
			t.Errorf("height %d: TranscriptRows = %d, want at least one", height, want)
		}
	}
}

// TestRenderClipsTranscriptToItsRows proves the output pane clamps the
// transcript to the rows the chrome leaves, keeping the newest row.
func TestRenderClipsTranscriptToItsRows(t *testing.T) {
	pane := tui.NewTranscriptPane()
	var events []tui.TranscriptEvent
	for i := range 20 {
		events = append(events, tui.TranscriptEvent{AgentRunID: 7, Seq: i, Type: "MESSAGE", Text: fmt.Sprintf("event %d", i)})
	}
	pane.SetView(tui.TranscriptViewModel{AtTail: true, RunOrder: []int64{7}, Events: events})
	vm := tui.ViewModel{
		Workers:    []tui.WorkerRow{{IssueID: "#1", State: domain.StateImplementing, Agents: tui.DeriveAgents(nil)}},
		Transcript: pane,
		Height:     12,
		Width:      100,
	}
	got := tui.Render(vm)
	lines := splitLines(got)
	if len(lines) != 12 {
		t.Fatalf("Render emitted %d rows in a 12-row terminal:\n%s", len(lines), got)
	}
	if !strings.Contains(got, "event 19") {
		t.Errorf("clipping dropped the newest transcript row:\n%s", got)
	}
	if strings.Contains(got, "event 0 ") || strings.Contains(got, "event 0│") {
		t.Errorf("clipping kept the oldest row instead of the newest:\n%s", got)
	}
	if rows := tui.TranscriptRows(vm); rows < 1 || rows > 12 {
		t.Errorf("TranscriptRows = %d, want a positive budget within the terminal", rows)
	}
}

// TestRenderTinyHeightKeepsOneTranscriptRow proves a terminal too short for
// the chrome still shows the newest transcript row and never panics.
func TestRenderTinyHeightKeepsOneTranscriptRow(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{AtTail: true, RunOrder: []int64{7}, Events: []tui.TranscriptEvent{
		{AgentRunID: 7, Seq: 0, Type: "MESSAGE", Text: "starting work"},
		{AgentRunID: 7, Seq: 1, Type: "MESSAGE", Text: "still working"},
	}})
	vm := tui.ViewModel{
		Workers:    []tui.WorkerRow{{IssueID: "#1", State: domain.StateImplementing}},
		Transcript: pane,
		Height:     1,
		Width:      80,
	}
	got := tui.Render(vm)
	if strings.Contains(got, "starting work") {
		t.Errorf("a one-row height drew the whole transcript:\n%s", got)
	}
	if !strings.Contains(got, "still working") {
		t.Errorf("a one-row height dropped the newest row:\n%s", got)
	}
	if rows := tui.TranscriptRows(vm); rows != 1 {
		t.Errorf("TranscriptRows = %d, want the floor of 1", rows)
	}
}

// TestRenderZeroHeightDrawsTheWholeTranscript proves an unset height clips
// nothing: the runtime sends no size before the first frame.
func TestRenderZeroHeightDrawsTheWholeTranscript(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{AtTail: true, RunOrder: []int64{7}, Events: []tui.TranscriptEvent{
		{AgentRunID: 7, Seq: 0, Type: "MESSAGE", Text: "starting work"},
		{AgentRunID: 7, Seq: 1, Type: "MESSAGE", Text: "still working"},
	}})
	vm := tui.ViewModel{Transcript: pane}
	if got := tui.Render(vm); !strings.Contains(got, "starting work") {
		t.Errorf("an unset height clipped the transcript:\n%s", got)
	}
	if rows := tui.TranscriptRows(vm); rows != 0 {
		t.Errorf("TranscriptRows = %d with no height, want 0 (no clip)", rows)
	}
}

// TestRenderKeepsAnExpandedPinnedSelectionOnScreen proves the frame keeps the
// operator's pinned selection visible, footer and all, even when its own
// expansion draws far more lines than the terminal has rows.
func TestRenderKeepsAnExpandedPinnedSelectionOnScreen(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{
		AtTail:   true,
		RunOrder: []int64{7},
		Events: []tui.TranscriptEvent{
			{AgentRunID: 7, Seq: 0, Type: "TOOL_CALL", ToolName: "bash", ToolInput: strings.Repeat("line\n", 50), ToolCallID: "t1"},
			{AgentRunID: 7, Seq: 1, Type: "MESSAGE", Text: "still working"},
		},
	})
	pane.Select(0)
	pane.ToggleExpand()
	vm := tui.ViewModel{
		Workers:    []tui.WorkerRow{{IssueID: "#1", State: domain.StateImplementing}},
		Transcript: pane,
		Focus:      tui.PaneTranscript,
		Height:     12,
		Width:      100,
	}
	got := splitLines(tui.Render(vm))
	if len(got) > 12 {
		t.Fatalf("Render emitted %d rows in a 12-row terminal:\n%s", len(got), strings.Join(got, "\n"))
	}
	if !strings.Contains(got[len(got)-1], "[q] quit") {
		t.Errorf("the expansion pushed the footer off screen:\n%s", strings.Join(got, "\n"))
	}
}

// TestRenderTranscriptWidthLeavesRoomForTheSidePanes proves the output pane's
// wrap width is the terminal width less the agents pane, the diff pane when
// open, and the borders, so a wrapped line never breaks the frame.
func TestRenderTranscriptWidthLeavesRoomForTheSidePanes(t *testing.T) {
	vm := layoutFixture()
	vm.Width = 120
	open := tui.TranscriptWidth(vm)
	vm.DiffOpen = false
	closed := tui.TranscriptWidth(vm)
	if open <= 0 || closed <= open || closed >= 120 {
		t.Fatalf("TranscriptWidth open %d closed %d, want 0 < open < closed < 120", open, closed)
	}
	vm.Width = 120
	vm.DiffOpen = true
	for _, line := range splitLines(tui.Render(vm)) {
		if w := lipgloss.Width(line); w > 120 {
			t.Errorf("row wider than the terminal (%d): %q", w, line)
		}
	}
}

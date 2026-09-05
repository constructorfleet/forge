package tui_test

import (
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tui"
)

// call builds a TOOL_CALL pane event.
func call(seq int, id, name, input string) tui.TranscriptEvent {
	return tui.TranscriptEvent{Seq: seq, Type: "TOOL_CALL", Role: "assistant", ToolName: name, ToolInput: input, ToolCallID: id}
}

// result builds a TOOL_RESULT pane event.
func result(seq int, id, name, output string) tui.TranscriptEvent {
	return tui.TranscriptEvent{Seq: seq, Type: "TOOL_RESULT", Role: "user", ToolName: name, ToolOutput: output, ToolCallID: id}
}

// prose builds a MESSAGE pane event.
func prose(seq int, text string) tui.TranscriptEvent {
	return tui.TranscriptEvent{Seq: seq, Type: "MESSAGE", Role: "assistant", Text: text}
}

func TestTranscriptGlyph(t *testing.T) {
	cases := []struct {
		name  string
		event tui.TranscriptEvent
		want  string
	}{
		{"call", call(0, "a", "bash", "ls"), "▸"},
		{"result", result(1, "a", "bash", "ok"), "└"},
		{"truncation", tui.TranscriptEvent{Seq: 2, Type: "TRUNCATION", Text: "5 dropped"}, "░"},
		{"prose has no glyph", prose(3, "hello"), " "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tui.TranscriptGlyph(tc.event); got != tc.want {
				t.Fatalf("TranscriptGlyph(%s) = %q, want %q", tc.event.Type, got, tc.want)
			}
		})
	}
}

// TestPaneCollapsedToolCall proves collapsed is the resting state: one line
// for the call plus the first output line of its result, nothing more.
func TestPaneCollapsedToolCall(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{Events: []tui.TranscriptEvent{
		call(0, "t1", "bash", "go test ./..."),
		result(1, "t1", "bash", "ok forge 0.1s\nok forge/internal 0.2s\nFAIL other"),
	}})

	got := tui.RenderTranscript(pane)
	lines := nonEmptyLines(got)
	if len(lines) != 2 {
		t.Fatalf("collapsed render has %d lines, want 2:\n%s", len(lines), got)
	}
	if !strings.Contains(lines[0], "▸") || !strings.Contains(lines[0], "bash") {
		t.Errorf("call line = %q, want the call glyph and the tool name", lines[0])
	}
	if !strings.Contains(lines[1], "ok forge 0.1s") {
		t.Errorf("result line = %q, want the first output line", lines[1])
	}
	if strings.Contains(got, "ok forge/internal") || strings.Contains(got, "FAIL other") {
		t.Errorf("collapsed render leaks later output lines:\n%s", got)
	}
	if strings.Contains(got, "go test ./...") {
		t.Errorf("collapsed render leaks the call input:\n%s", got)
	}
}

// TestRenderTranscriptWrapsLongLines proves a line longer than the pane's set
// width renders as the several rows the terminal would itself wrap it into,
// so a row-based clip downstream counts it correctly.
func TestRenderTranscriptWrapsLongLines(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{Events: []tui.TranscriptEvent{
		prose(0, strings.Repeat("x", 30)),
	}})

	unwrapped := nonEmptyLines(tui.RenderTranscript(pane))
	if len(unwrapped) != 1 {
		t.Fatalf("render with no width set has %d lines, want 1:\n%s", len(unwrapped), strings.Join(unwrapped, "\n"))
	}

	pane.SetWidth(10)
	wrapped := nonEmptyLines(tui.RenderTranscript(pane))
	if len(wrapped) < 3 {
		t.Fatalf("render at width 10 has %d lines, want at least 3:\n%s", len(wrapped), strings.Join(wrapped, "\n"))
	}
	for _, l := range wrapped {
		if len([]rune(l)) > 10 {
			t.Errorf("wrapped line %q is %d runes, want at most 10", l, len([]rune(l)))
		}
	}
}

// TestRenderTranscriptWrapsStyledLines proves a styled header line (the
// default colour scheme renders a tool call in Faint) that is longer than the
// pane's set width still wraps into rows of at most the set width in visible
// cells, with every SGR escape sequence intact, rather than splitting inside
// an escape code.
func TestRenderTranscriptWrapsStyledLines(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetStyle(tui.DefaultStyle())
	pane.SetView(tui.TranscriptViewModel{Events: []tui.TranscriptEvent{
		call(0, "t1", strings.Repeat("x", 30), "ls"),
	}})
	pane.SetWidth(10)

	wrapped := nonEmptyLines(tui.RenderTranscript(pane))
	if len(wrapped) < 3 {
		t.Fatalf("styled render at width 10 has %d lines, want at least 3:\n%s", len(wrapped), strings.Join(wrapped, "\n"))
	}
	for _, l := range wrapped {
		if strings.Count(l, "\x1b[") != strings.Count(l, "m") {
			t.Errorf("wrapped styled line %q contains an unterminated escape sequence", l)
		}
	}
}

// TestPaneExpandShowsCallAndResult proves expansion reveals the full call
// input and the full result output.
func TestPaneExpandShowsCallAndResult(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{Events: []tui.TranscriptEvent{
		call(0, "t1", "bash", "go test ./..."),
		result(1, "t1", "bash", "line one\nline two"),
	}})
	pane.ToggleExpand()

	got := tui.RenderTranscript(pane)
	for _, want := range []string{"go test ./...", "line one", "line two"} {
		if !strings.Contains(got, want) {
			t.Errorf("expanded render is missing %q:\n%s", want, got)
		}
	}
}

// TestPaneExpansionResetsOnSelectionChange proves expansion is per entry: a
// selection move collapses what was open, and moving back does not reopen it.
func TestPaneExpansionResetsOnSelectionChange(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{Events: []tui.TranscriptEvent{
		call(0, "t1", "bash", "first input"),
		result(1, "t1", "bash", "first output"),
		call(2, "t2", "read", "second input"),
		result(3, "t2", "read", "second output"),
	}})

	pane.Select(0)
	pane.ToggleExpand()
	if !strings.Contains(tui.RenderTranscript(pane), "first input") {
		t.Fatalf("entry 0 did not expand:\n%s", tui.RenderTranscript(pane))
	}

	pane.Select(1)
	if got := tui.RenderTranscript(pane); strings.Contains(got, "first input") {
		t.Errorf("expansion survived a selection change:\n%s", got)
	}
	if got := tui.RenderTranscript(pane); strings.Contains(got, "second input") {
		t.Errorf("the new selection opened expanded, want collapsed at rest:\n%s", got)
	}

	pane.Select(0)
	if got := tui.RenderTranscript(pane); strings.Contains(got, "first input") {
		t.Errorf("expansion returned with the selection, want collapsed:\n%s", got)
	}
}

// TestPaneToggleCollapsesAgain proves the toggle is symmetric.
func TestPaneToggleCollapsesAgain(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{Events: []tui.TranscriptEvent{
		call(0, "t1", "bash", "the input"),
		result(1, "t1", "bash", "the output"),
	}})

	pane.ToggleExpand()
	pane.ToggleExpand()
	if got := tui.RenderTranscript(pane); strings.Contains(got, "the input") {
		t.Errorf("second toggle did not collapse:\n%s", got)
	}
}

// TestPaneProseIsNotExpandable proves only tool calls expand.
func TestPaneProseIsNotExpandable(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{Events: []tui.TranscriptEvent{prose(0, "thinking about it")}})

	if pane.CanExpand() {
		t.Fatal("CanExpand on a prose entry = true, want false")
	}
	pane.ToggleExpand()
	if pane.Expanded(0) {
		t.Error("a prose entry expanded")
	}
}

// TestPaneEvictionWordingIsDistinctFromTruncation proves the reader-side gap
// and the Agent's own bounded-transcript marker never read as the same thing.
func TestPaneEvictionWordingIsDistinctFromTruncation(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{
		Evicted: true,
		Dropped: 12,
		Events: []tui.TranscriptEvent{
			{Seq: 40, Type: "TRUNCATION", Text: "5 dropped"},
			prose(41, "carrying on"),
		},
	})

	got := tui.RenderTranscript(pane)
	if !strings.Contains(got, "not retained") {
		t.Errorf("render is missing the eviction marker:\n%s", got)
	}
	if !strings.Contains(got, "░ earlier events not retained") {
		t.Errorf("eviction marker uses the wrong glyph:\n%s", got)
	}
	if !strings.Contains(got, "12") {
		t.Errorf("eviction marker omits the dropped count:\n%s", got)
	}
	if !strings.Contains(got, "truncated by the agent") {
		t.Errorf("render is missing the truncation marker:\n%s", got)
	}
	if strings.Contains(got, "not retained by the agent") {
		t.Errorf("the two markers share wording:\n%s", got)
	}
}

// TestPaneUnpairedResultStaysVisible proves a result whose call fell out of
// the window still renders, so eviction never hides work that did happen.
func TestPaneUnpairedResultStaysVisible(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{Events: []tui.TranscriptEvent{
		result(9, "gone", "bash", "orphan output\nmore"),
	}})

	if len(pane.Entries()) != 1 {
		t.Fatalf("entries = %d, want 1", len(pane.Entries()))
	}
	got := tui.RenderTranscript(pane)
	if !strings.Contains(got, "└") || !strings.Contains(got, "orphan output") {
		t.Errorf("unpaired result render = %q, want the result glyph and its first line", got)
	}
}

// TestPaneSelectionHoldsItsEntryAsTheTailGrows proves a poll that appends
// events leaves the operator's selection where they put it.
func TestPaneSelectionHoldsItsEntryAsTheTailGrows(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{Events: []tui.TranscriptEvent{
		prose(0, "first"),
		prose(1, "second"),
	}})
	pane.Select(0)

	pane.SetView(tui.TranscriptViewModel{Events: []tui.TranscriptEvent{
		prose(0, "first"),
		prose(1, "second"),
		prose(2, "third"),
	}})

	e, ok := pane.SelectedEntry()
	if !ok || e.Event.Seq != 0 {
		t.Fatalf("selected seq = %d (ok=%v), want 0", e.Event.Seq, ok)
	}
}

// transcriptFixture builds a pane over one prose event and two tool calls.
func transcriptFixture() *tui.TranscriptPane {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{AtTail: true, Events: []tui.TranscriptEvent{
		prose(0, "starting work"),
		call(1, "t1", "bash", "go build ./..."),
		result(2, "t1", "bash", "build ok"),
	}})
	return pane
}

// TestTranscriptKeysAreTranscriptLegalOnly proves the footer never advertises
// a roster action while the transcript pane holds focus.
func TestTranscriptKeysAreTranscriptLegalOnly(t *testing.T) {
	pane := transcriptFixture()
	pane.Select(1)

	keys := map[string]string{}
	for _, k := range tui.TranscriptKeys(pane) {
		keys[k.Key] = k.Label
	}
	for _, want := range []string{"q", "tab", "enter", "k", "j"} {
		if _, ok := keys[want]; !ok {
			t.Errorf("TranscriptKeys omits %q, got %v", want, keys)
		}
	}
	for _, illegal := range []string{"c", "r", "a"} {
		if _, ok := keys[illegal]; ok {
			t.Errorf("TranscriptKeys advertises the roster key %q", illegal)
		}
	}
	if keys["enter"] != "expand" {
		t.Errorf("enter label = %q, want \"expand\" on a collapsed tool call", keys["enter"])
	}

	pane.ToggleExpand()
	for _, k := range tui.TranscriptKeys(pane) {
		if k.Key == "enter" && k.Label != "collapse" {
			t.Errorf("enter label = %q, want \"collapse\" on an expanded tool call", k.Label)
		}
	}
}

// TestTranscriptKeysHideExpandOnProse proves an entry with nothing to expand
// never advertises the expand key.
func TestTranscriptKeysHideExpandOnProse(t *testing.T) {
	pane := transcriptFixture()
	pane.Select(0)

	for _, k := range tui.TranscriptKeys(pane) {
		if k.Key == "enter" {
			t.Fatalf("TranscriptKeys advertises enter on a prose entry")
		}
	}
}

// fakeScroller counts the scrollback requests the pane forwards.
type fakeScroller struct {
	tails int
	up    int
	down  int
}

func (s *fakeScroller) ScrollToTail() { s.tails++ }
func (s *fakeScroller) ScrollUp(n int) {
	s.up += n
}
func (s *fakeScroller) ScrollDown(n int) { s.down += n }
func (s *fakeScroller) PageSize() int    { return 10 }

// TestPaneFollowsTailUntilOperatorSelects proves an unpinned pane keeps
// following the live tail, and that one operator move pins the selection.
func TestPaneFollowsTailUntilOperatorSelects(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{AtTail: true, Events: []tui.TranscriptEvent{
		prose(0, "first"),
		prose(1, "second"),
	}})

	pane.SetView(tui.TranscriptViewModel{AtTail: true, Events: []tui.TranscriptEvent{
		prose(0, "first"),
		prose(1, "second"),
		prose(2, "third"),
	}})
	if e, _ := pane.SelectedEntry(); e.Event.Seq != 2 {
		t.Fatalf("selected seq = %d, want 2: the pane stopped following the tail", e.Event.Seq)
	}

	pane.Select(0)
	pane.SetView(tui.TranscriptViewModel{AtTail: true, Events: []tui.TranscriptEvent{
		prose(0, "first"),
		prose(1, "second"),
		prose(2, "third"),
		prose(3, "fourth"),
	}})
	if e, _ := pane.SelectedEntry(); e.Event.Seq != 0 {
		t.Fatalf("selected seq = %d, want 0: the tail moved an operator selection", e.Event.Seq)
	}
}

// TestPaneFollowTailUnpinsTheSelection proves the follow-tail action scrolls
// the tailer, collapses, selects the newest entry, and resumes tail following.
func TestPaneFollowTailUnpinsTheSelection(t *testing.T) {
	sc := &fakeScroller{}
	pane := tui.NewTranscriptPane()
	pane.SetScroller(sc)
	pane.SetView(tui.TranscriptViewModel{AtTail: false, Events: []tui.TranscriptEvent{
		call(0, "t1", "bash", "the input"),
		result(1, "t1", "bash", "the output"),
		prose(2, "carrying on"),
	}})
	pane.Select(0)
	pane.ToggleExpand()

	pane.FollowTail()
	if sc.tails != 1 {
		t.Fatalf("ScrollToTail calls = %d, want 1", sc.tails)
	}
	if e, _ := pane.SelectedEntry(); e.Event.Seq != 2 {
		t.Errorf("selected seq = %d, want 2 after follow tail", e.Event.Seq)
	}
	if got := tui.RenderTranscript(pane); strings.Contains(got, "the input") {
		t.Errorf("follow tail left an entry expanded:\n%s", got)
	}

	pane.SetView(tui.TranscriptViewModel{AtTail: true, Events: []tui.TranscriptEvent{
		call(0, "t1", "bash", "the input"),
		result(1, "t1", "bash", "the output"),
		prose(2, "carrying on"),
		prose(3, "newest"),
	}})
	if e, _ := pane.SelectedEntry(); e.Event.Seq != 3 {
		t.Errorf("selected seq = %d, want 3: follow tail did not unpin", e.Event.Seq)
	}
}

// TestTranscriptKeysHideTailWithoutScroller proves the footer never advertises
// a follow-tail the pane cannot perform.
func TestTranscriptKeysHideTailWithoutScroller(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{AtTail: false, Events: []tui.TranscriptEvent{prose(0, "old")}})

	for _, k := range tui.TranscriptKeys(pane) {
		if k.Key == "G" {
			t.Fatal("TranscriptKeys advertises follow tail with no scroller attached")
		}
	}
}

// TestTranscriptKeysOfferTailWhenScrolledBack proves the follow-tail key
// appears only when the window has left the tail.
func TestTranscriptKeysOfferTailWhenScrolledBack(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetScroller(&fakeScroller{})
	pane.SetView(tui.TranscriptViewModel{AtTail: false, Events: []tui.TranscriptEvent{prose(0, "old")}})

	var found bool
	for _, k := range tui.TranscriptKeys(pane) {
		if k.Key == "G" {
			found = true
		}
	}
	if !found {
		t.Errorf("TranscriptKeys omits the follow-tail key while scrolled back: %v", tui.TranscriptKeys(pane))
	}
}

// TestFrameRendersTranscriptPaneAndItsFooter proves the pane is part of the
// same pure frame as the roster, and that focus moves the detail strip and
// the footer onto the transcript.
func TestFrameRendersTranscriptPaneAndItsFooter(t *testing.T) {
	pane := transcriptFixture()
	pane.Select(1)
	vm := tui.ViewModel{
		Workers: []tui.WorkerRow{{
			IssueID: "issue-1",
			Title:   "do the thing",
			State:   domain.StateFailed,
			Attempt: 2,
			Budget:  3,
		}},
		Transcript: pane,
		Focus:      tui.PaneTranscript,
	}

	got := tui.Render(vm)
	if !strings.Contains(got, "do the thing") {
		t.Errorf("frame lost the roster row:\n%s", got)
	}
	if !strings.Contains(got, "bash") || !strings.Contains(got, "build ok") {
		t.Errorf("frame lost the transcript pane:\n%s", got)
	}
	if !strings.Contains(got, "seq 1") || !strings.Contains(got, "TOOL_CALL") {
		t.Errorf("detail strip does not describe the transcript selection:\n%s", got)
	}
	if strings.Contains(got, "[r] retry") {
		t.Errorf("footer advertises a roster key while the transcript has focus:\n%s", got)
	}
	if !strings.Contains(got, "[enter] expand") {
		t.Errorf("footer omits the transcript expand key:\n%s", got)
	}
}

// TestFrameRosterFocusKeepsRosterFooter proves focus on the roster leaves the
// worker detail strip and the state-legal keys in place.
func TestFrameRosterFocusKeepsRosterFooter(t *testing.T) {
	vm := tui.ViewModel{
		Workers:    []tui.WorkerRow{{IssueID: "issue-1", Title: "t", State: domain.StateFailed, Attempt: 1, Budget: 3}},
		Transcript: transcriptFixture(),
		Focus:      tui.PaneRoster,
	}

	got := tui.Render(vm)
	if !strings.Contains(got, "[r] retry") {
		t.Errorf("footer omits the roster retry key:\n%s", got)
	}
	if !strings.Contains(got, "attempt 1/3") {
		t.Errorf("detail strip is not the worker strip:\n%s", got)
	}
	if strings.Contains(got, "[enter] expand") {
		t.Errorf("footer advertises a transcript key while the roster has focus:\n%s", got)
	}
}

// TestRenderEmptyRosterDoesNotPanic proves a frame with no Workers still
// renders, which a transcript-only attach needs.
func TestRenderEmptyRosterDoesNotPanic(t *testing.T) {
	if got := tui.Render(tui.ViewModel{}); got == "" {
		t.Error("Render on an empty view-model returned nothing, want at least a footer")
	}
}

// TestRenderTranscriptNilPane proves the pane renderer tolerates a nil pane,
// as TranscriptKeys does.
func TestRenderTranscriptNilPane(t *testing.T) {
	if got := tui.RenderTranscript(nil); got != "" {
		t.Errorf("RenderTranscript(nil) = %q, want the empty string", got)
	}
}

// TestPaneGlyphsFollowTheAgentEventTypes proves the pane's own event-type
// names still match the producer's, so a rename in the agent package fails
// here instead of silently rendering every event as prose.
func TestPaneGlyphsFollowTheAgentEventTypes(t *testing.T) {
	cases := []struct {
		typ  agent.TranscriptEventType
		want string
	}{
		{agent.TranscriptEventToolCall, "▸"},
		{agent.TranscriptEventToolResult, "└"},
		{agent.TranscriptEventTruncation, "░"},
		{agent.TranscriptEventMessage, " "},
	}
	for _, tc := range cases {
		got := tui.TranscriptGlyph(tui.TranscriptEvent{Type: string(tc.typ)})
		if got != tc.want {
			t.Errorf("TranscriptGlyph(%s) = %q, want %q", tc.typ, got, tc.want)
		}
	}
}

// TestTailerSatisfiesTheScrollerSeam proves the live tailer is the pane's
// scroller, so the pane's scrollback requests reach the retained window.
func TestTailerSatisfiesTheScrollerSeam(t *testing.T) {
	var _ tui.TranscriptScroller = tui.NewTranscriptTailer(nil, 1, 8)
}

// TestPaneMoveUpAtTopEdgePagesOlderEvents proves the selection can leave the
// current window: a move past the leading edge scrolls the tailer back, and the
// wider window then carries the selection onto the older entry.
func TestPaneMoveUpAtTopEdgePagesOlderEvents(t *testing.T) {
	sc := &fakeScroller{}
	pane := tui.NewTranscriptPane()
	pane.SetScroller(sc)
	pane.SetView(tui.TranscriptViewModel{AtTail: true, Retained: 3, Events: []tui.TranscriptEvent{
		prose(1, "second"),
		prose(2, "third"),
	}})
	pane.Select(0)

	pane.MoveSelection(-1)
	if sc.up != 1 {
		t.Fatalf("ScrollUp events = %d, want 1: the top edge did not page back", sc.up)
	}

	// The tailer answers with the window one event older.
	pane.SetView(tui.TranscriptViewModel{AtTail: false, Retained: 3, Events: []tui.TranscriptEvent{
		prose(0, "first"),
		prose(1, "second"),
	}})
	if e, _ := pane.SelectedEntry(); e.Event.Seq != 0 {
		t.Errorf("selected seq = %d, want 0: the pending move did not apply", e.Event.Seq)
	}
}

// TestPaneMoveUpAtRetainedStartDoesNotPin proves an up key on the oldest
// entry, with no older event retained, leaves the pane following the tail. The
// tailer clamps such a scroll to a no-op, so a pin here would silently stop the
// pane from following the live tail after a key press that moved nothing.
func TestPaneMoveUpAtRetainedStartDoesNotPin(t *testing.T) {
	sc := &fakeScroller{}
	pane := tui.NewTranscriptPane()
	pane.SetScroller(sc)
	pane.SetView(tui.TranscriptViewModel{AtTail: true, AtStart: true, Retained: 2, Events: []tui.TranscriptEvent{
		prose(0, "first"),
		prose(1, "second"),
	}})
	pane.Select(0)

	pane.MoveSelection(-1)
	if sc.up != 0 {
		t.Errorf("ScrollUp events = %d, want 0 at the retained start", sc.up)
	}

	pane.SetView(tui.TranscriptViewModel{AtTail: true, AtStart: true, Retained: 3, Events: []tui.TranscriptEvent{
		prose(0, "first"),
		prose(1, "second"),
		prose(2, "third"),
	}})
	if e, _ := pane.SelectedEntry(); e.Event.Seq != 0 {
		t.Errorf("selected seq = %d, want 0: the operator's own Select must still hold", e.Event.Seq)
	}
}

// TestPaneMoveDownAtTailDoesNotPin proves a down key on the newest entry, with
// nothing newer to reach, leaves the pane following the tail.
func TestPaneMoveDownAtTailDoesNotPin(t *testing.T) {
	sc := &fakeScroller{}
	pane := tui.NewTranscriptPane()
	pane.SetScroller(sc)
	pane.SetView(tui.TranscriptViewModel{AtTail: true, Events: []tui.TranscriptEvent{
		prose(0, "first"),
		prose(1, "second"),
	}})

	pane.MoveSelection(1)
	if sc.down != 0 {
		t.Errorf("ScrollDown events = %d, want 0 at the tail", sc.down)
	}
	for _, k := range tui.TranscriptKeys(pane) {
		if k.Key == "G" {
			t.Error("footer offers follow tail after an inert down key")
		}
	}

	pane.SetView(tui.TranscriptViewModel{AtTail: true, Events: []tui.TranscriptEvent{
		prose(0, "first"),
		prose(1, "second"),
		prose(2, "third"),
	}})
	if e, _ := pane.SelectedEntry(); e.Event.Seq != 2 {
		t.Errorf("selected seq = %d, want 2: an inert down key pinned the selection", e.Event.Seq)
	}
}

// TestPaneMoveSelectionPage proves the selection moves by the viewport height
// (the page size) when requested. A page up at the top edge scrolls the window
// back by a page; a page down at the bottom edge scrolls forward by a page.
func TestPaneMoveSelectionPage(t *testing.T) {
	sc := &fakeScroller{}
	pane := tui.NewTranscriptPane()
	pane.SetScroller(sc)
	// PageSize returns 10 for fakeScroller.
	pane.SetView(tui.TranscriptViewModel{AtTail: true, Retained: 20, Events: []tui.TranscriptEvent{
		prose(15, "e15"), prose(16, "e16"), prose(17, "e17"), prose(18, "e18"), prose(19, "e19"),
	}})
	pane.Select(4) // e19

	// Page up moves back by a page (10 events).
	pane.MoveSelectionPage(-1)
	if sc.up != 10 {
		t.Fatalf("ScrollUp events = %d, want 10: page up did not scroll by page size", sc.up)
	}

	// The tailer answers with the window 10 events older.
	pane.SetView(tui.TranscriptViewModel{AtTail: false, Retained: 20, Events: []tui.TranscriptEvent{
		prose(5, "e5"), prose(6, "e6"), prose(7, "e7"), prose(8, "e8"), prose(9, "e9"),
	}})
	if e, _ := pane.SelectedEntry(); e.Event.Seq != 5 {
		t.Errorf("selected seq = %d, want 5: the pending page move did not apply", e.Event.Seq)
	}

	// Page down moves forward by a page (10 events).
	pane.MoveSelectionPage(1)
	if sc.down != 10 {
		t.Fatalf("ScrollDown events = %d, want 10: page down did not scroll by page size", sc.down)
	}

	// The tailer answers with the window 10 events newer.
	pane.SetView(tui.TranscriptViewModel{AtTail: true, Retained: 20, Events: []tui.TranscriptEvent{
		prose(15, "e15"), prose(16, "e16"), prose(17, "e17"), prose(18, "e18"), prose(19, "e19"),
	}})
	if e, _ := pane.SelectedEntry(); e.Event.Seq != 19 {
		t.Errorf("selected seq = %d, want 19: the pending page move did not apply", e.Event.Seq)
	}
}

// TestPaneMoveSelectionPageAtEdgesDoesNotPin proves a page scroll past the
// retained start or tail clamps and does not pin the selection.
func TestPaneMoveSelectionPageAtEdgesDoesNotPin(t *testing.T) {
	sc := &fakeScroller{}
	pane := tui.NewTranscriptPane()
	pane.SetScroller(sc)
	pane.SetView(tui.TranscriptViewModel{AtTail: true, AtStart: true, Retained: 5, Events: []tui.TranscriptEvent{
		prose(0, "e0"), prose(1, "e1"), prose(2, "e2"), prose(3, "e3"), prose(4, "e4"),
	}})
	pane.Select(0)

	// Page up at retained start does nothing.
	pane.MoveSelectionPage(-1)
	if sc.up != 0 {
		t.Errorf("ScrollUp events = %d, want 0 at the retained start", sc.up)
	}
	if _, ok := pane.SelectedEntry(); !ok {
		t.Errorf("selected entry missing at retained start")
	} else if e, _ := pane.SelectedEntry(); e.Event.Seq != 0 {
		t.Errorf("selected seq = %d, want 0: page up at start pinned the selection", e.Event.Seq)
	}

	// Page down at tail does nothing.
	pane.SetView(tui.TranscriptViewModel{AtTail: true, Retained: 5, Events: []tui.TranscriptEvent{
		prose(0, "e0"), prose(1, "e1"), prose(2, "e2"), prose(3, "e3"), prose(4, "e4"),
	}})
	pane.Select(4)
	pane.MoveSelectionPage(1)
	if sc.down != 0 {
		t.Errorf("ScrollDown events = %d, want 0 at the tail", sc.down)
	}
	pane.SetView(tui.TranscriptViewModel{AtTail: true, Retained: 5, Events: []tui.TranscriptEvent{
		prose(0, "e0"), prose(1, "e1"), prose(2, "e2"), prose(3, "e3"), prose(4, "e4"),
	}})
	if e, _ := pane.SelectedEntry(); e.Event.Seq != 4 {
		t.Errorf("selected seq = %d, want 4: page down at tail pinned the selection", e.Event.Seq)
	}
}

// TestPaneFollowTailClearsTheFollowTailKey proves the footer reflects the
// pane's own action at once, without waiting for the next poll.
func TestPaneFollowTailClearsTheFollowTailKey(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetScroller(&fakeScroller{})
	pane.SetView(tui.TranscriptViewModel{AtTail: false, Events: []tui.TranscriptEvent{prose(0, "old")}})

	pane.FollowTail()
	for _, k := range tui.TranscriptKeys(pane) {
		if k.Key == "G" {
			t.Error("footer still offers follow tail after the pane followed the tail")
		}
	}
}

// TestPanePinnedSelectionAnchorsTheWindow proves a pinned selection that the
// advancing tail pushes out of the window anchors the window instead of
// jumping to the newest entry.
func TestPanePinnedSelectionAnchorsTheWindow(t *testing.T) {
	sc := &fakeScroller{}
	pane := tui.NewTranscriptPane()
	pane.SetScroller(sc)
	pane.SetView(tui.TranscriptViewModel{AtTail: true, Retained: 2, Events: []tui.TranscriptEvent{
		prose(0, "first"),
		prose(1, "second"),
	}})
	pane.Select(0)

	// The window slides: seq 0 is no longer visible.
	pane.SetView(tui.TranscriptViewModel{AtTail: true, Retained: 3, Events: []tui.TranscriptEvent{
		prose(1, "second"),
		prose(2, "third"),
	}})
	if sc.up != 1 {
		t.Errorf("ScrollUp events = %d, want 1: the pane did not anchor the window", sc.up)
	}
	if e, _ := pane.SelectedEntry(); e.Event.Seq != 1 {
		t.Errorf("selected seq = %d, want 1: the lost selection jumped to the tail", e.Event.Seq)
	}

	// The anchored window brings the pinned entry back.
	pane.SetView(tui.TranscriptViewModel{AtTail: false, Retained: 3, Events: []tui.TranscriptEvent{
		prose(0, "first"),
		prose(1, "second"),
	}})
	if e, _ := pane.SelectedEntry(); e.Event.Seq != 1 {
		t.Errorf("selected seq = %d, want 1: the pane lost its pinned entry", e.Event.Seq)
	}
}

// TestPaneEmptyToolOutputHasNoBlankLine proves a tool that returns nothing
// renders one line, not a line plus whitespace.
func TestPaneEmptyToolOutputHasNoBlankLine(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{Events: []tui.TranscriptEvent{
		call(0, "t1", "bash", "touch f"),
		result(1, "t1", "bash", ""),
	}})

	lines := strings.Split(strings.TrimRight(tui.RenderTranscript(pane), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("render has %d lines, want 1:\n%q", len(lines), lines)
	}
}

// nonEmptyLines splits a render into its non-blank lines.
func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// visible removes SGR escape sequences, so a test can match the text a live
// frame draws independent of the colour scheme the live view applies.
func visible(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestRenderTranscriptDividesAdjacentAttempts proves a retry reads as one
// continuous scrollback: the pane puts an inline "attempt N" divider at each
// run boundary, numbered by run insertion order.
func TestRenderTranscriptDividesAdjacentAttempts(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{
		AtTail:   true,
		RunOrder: []int64{4, 9},
		Events: []tui.TranscriptEvent{
			{AgentRunID: 4, Seq: 0, Type: "MESSAGE", Text: "first try"},
			{AgentRunID: 9, Seq: 0, Type: "MESSAGE", Text: "second try"},
		},
	})

	got := tui.RenderTranscript(pane)
	want := "── attempt 1 ──\n" +
		"    first try\n" +
		"── attempt 2 ──\n" +
		">   second try\n"
	if got != want {
		t.Errorf("RenderTranscript() =\n%q\nwant\n%q", got, want)
	}
}

// TestRenderTranscriptOmitsDividerForOneAttempt proves a single-attempt history
// carries no divider: the annotation exists to separate attempts.
func TestRenderTranscriptOmitsDividerForOneAttempt(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{
		AtTail:   true,
		RunOrder: []int64{4},
		Events:   []tui.TranscriptEvent{{AgentRunID: 4, Seq: 0, Type: "MESSAGE", Text: "only try"}},
	})

	if got := tui.RenderTranscript(pane); strings.Contains(got, "attempt") {
		t.Errorf("RenderTranscript() = %q, want no attempt divider", got)
	}
}

// TestRenderTranscriptNumbersScrolledAttempt proves the divider number comes
// from the run's place in the whole history, not from the visible window: a
// window that starts inside attempt 3 still reads "attempt 3".
func TestRenderTranscriptNumbersScrolledAttempt(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{
		RunOrder: []int64{4, 9, 11},
		Events:   []tui.TranscriptEvent{{AgentRunID: 11, Seq: 0, Type: "MESSAGE", Text: "third try"}},
	})

	if got := tui.RenderTranscript(pane); !strings.Contains(got, "── attempt 3 ──") {
		t.Errorf("RenderTranscript() = %q, want an attempt 3 divider", got)
	}
}

// TestPaneSelectionHoldsAcrossAttemptsWithEqualSeq proves the selection anchor
// is the run plus the seq: every run restarts at seq 0, so a seq-only anchor
// would jump to the older attempt's event.
func TestPaneSelectionHoldsAcrossAttemptsWithEqualSeq(t *testing.T) {
	pane := tui.NewTranscriptPane()
	older := tui.TranscriptEvent{AgentRunID: 4, Seq: 0, Type: "MESSAGE", Text: "first try"}
	newer := tui.TranscriptEvent{AgentRunID: 9, Seq: 0, Type: "MESSAGE", Text: "second try"}
	pane.SetView(tui.TranscriptViewModel{RunOrder: []int64{4, 9}, Events: []tui.TranscriptEvent{older, newer}})
	pane.Select(1)

	pane.SetView(tui.TranscriptViewModel{
		RunOrder: []int64{4, 9},
		Events:   []tui.TranscriptEvent{older, newer, {AgentRunID: 9, Seq: 1, Type: "MESSAGE", Text: "more"}},
	})

	e, ok := pane.SelectedEntry()
	if !ok || e.Event.AgentRunID != 9 || e.Event.Seq != 0 {
		t.Errorf("SelectedEntry() = %+v (ok=%v), want run 9 seq 0", e.Event, ok)
	}
}

// TestPaneFoldsToolResultWithinItsOwnAttempt proves a repeated tool call id
// across attempts does not fold the retry's result into the first attempt's
// call: the pairing key includes the run.
func TestPaneFoldsToolResultWithinItsOwnAttempt(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{
		AtTail:   true,
		RunOrder: []int64{4, 9},
		Events: []tui.TranscriptEvent{
			{AgentRunID: 4, Seq: 0, Type: "TOOL_CALL", ToolName: "read", ToolCallID: "c1"},
			{AgentRunID: 9, Seq: 0, Type: "TOOL_RESULT", ToolName: "read", ToolCallID: "c1", ToolOutput: "retry out"},
		},
	})

	entries := pane.Entries()
	if len(entries) != 2 {
		t.Fatalf("Entries() = %d entries, want 2", len(entries))
	}
	if entries[0].Result != nil {
		t.Errorf("entry 0 folded a result from another attempt: %+v", entries[0].Result)
	}
}

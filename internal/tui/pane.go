package tui

// pane.go: transcript pane rendering plus collapse, expand, and selection
// state.

import (
	"fmt"
	"strings"
)

// Event type names the pane renders. They mirror agent.TranscriptEventType,
// repeated as plain strings so the TUI keeps no dependency on the agent
// package.
const (
	eventMessage    = "MESSAGE"
	eventToolCall   = "TOOL_CALL"
	eventToolResult = "TOOL_RESULT"
	eventTruncation = "TRUNCATION"
)

// TranscriptGlyph maps one event to its single-rune column. Prose carries no
// glyph, so a message reads as the Agent's own voice and not as machinery.
func TranscriptGlyph(e TranscriptEvent) string {
	switch e.Type {
	case eventToolCall:
		return "→"
	case eventToolResult:
		return "←"
	case eventTruncation:
		return "…"
	default:
		return " "
	}
}

// noSelection marks an empty pane, which has no selectable entry.
const noSelection = -1

// TranscriptEntry is one selectable timeline item. A tool call folds its
// paired result into the same entry, so collapse, expand, and selection all
// act on the call and its result together.
type TranscriptEntry struct {
	Event TranscriptEvent
	// Result is the paired TOOL_RESULT, when the call has returned and the
	// window retains it.
	Result *TranscriptEvent
}

// IsToolCall reports that the entry is a tool call, the only expandable kind.
func (e TranscriptEntry) IsToolCall() bool { return e.Event.Type == eventToolCall }

// TranscriptScroller is the window-anchor seam the pane needs to move the
// visible window. TranscriptTailer implements it.
type TranscriptScroller interface {
	// ScrollUp moves the window n events towards the retained start.
	ScrollUp(n int)
	// ScrollDown moves the window n events towards the tail.
	ScrollDown(n int)
	// ScrollToTail returns the window to following the tail.
	ScrollToTail()
}

// TranscriptPane holds the transcript window plus its selection and the one
// expanded entry. Expansion is per entry and never persists across a
// selection change: collapsed is the resting state.
type TranscriptPane struct {
	view    TranscriptViewModel
	entries []TranscriptEntry

	// scroller moves the window anchor. Nil means the pane cannot follow the
	// tail, and the footer then hides the follow-tail key.
	scroller TranscriptScroller

	selection int
	// expanded holds the selection index that is expanded, or noSelection.
	// It tracks the index rather than a flag so any selection move resets it.
	expanded int
	// pinned records that the operator chose the selection. An unpinned
	// selection follows the live tail as new events arrive.
	pinned bool
	// pendingMove holds a selection move the current window cannot satisfy.
	// The pane asks the scroller for a wider window and applies the move when
	// that window arrives, so one key press still moves one entry.
	pendingMove int
	// tailRequested records a follow-tail the next poll has not yet confirmed.
	// The pane holds its own intent here and never writes to view, which stays
	// the tailer's snapshot alone.
	tailRequested bool
}

// NewTranscriptPane returns an empty, fully collapsed pane.
func NewTranscriptPane() *TranscriptPane {
	return &TranscriptPane{selection: noSelection, expanded: noSelection}
}

// SetScroller attaches the window-anchor seam. The follow-tail key, a selection
// move past a window edge, and a lost pinned anchor all drive it.
func (p *TranscriptPane) SetScroller(s TranscriptScroller) { p.scroller = s }

// SetView replaces the visible window. A pinned selection holds its entry by
// Seq where the new window still retains it, so a poll that appends events does
// not move the operator's selection. An unpinned selection follows the tail
// while the window is at the tail. Expansion survives only where the selection
// holds the same event.
func (p *TranscriptPane) SetView(vm TranscriptViewModel) {
	prevSeq, hadSelection := p.selectedSeq()
	wasExpanded := p.expanded != noSelection
	pending := p.pendingMove
	p.pendingMove = 0
	p.tailRequested = false

	p.view = vm
	p.entries = buildEntries(vm.Events)

	idx := defaultSelection(p.entries)
	if hadSelection && (p.pinned || !vm.AtTail) {
		idx = indexOfSeq(p.entries, prevSeq)
		if idx == noSelection {
			idx = p.anchorLostSelection()
		}
	}
	if pending != 0 && idx != noSelection {
		idx = clampIndex(idx+pending, len(p.entries))
	}

	p.selection = idx
	p.expanded = noSelection
	if wasExpanded {
		if e, ok := p.SelectedEntry(); ok && e.Event.Seq == prevSeq {
			p.expanded = idx
		}
	}
}

// anchorLostSelection handles a pinned entry that the advancing tail pushed out
// of the window. It asks for one event of scrollback, which stops the window
// from sliding further, and holds the oldest retained entry meanwhile.
func (p *TranscriptPane) anchorLostSelection() int {
	if p.scroller == nil || len(p.entries) == 0 || p.view.AtStart {
		return defaultSelection(p.entries)
	}
	p.scroller.ScrollUp(1)
	return 0
}

// FollowTail returns the window to the live tail, collapses, and hands the
// selection back to the tail. It is the inverse of an operator selection. With
// no scroller attached it does nothing, so expansion and pinning both stand.
func (p *TranscriptPane) FollowTail() {
	if p.scroller == nil {
		return
	}
	p.scroller.ScrollToTail()
	// Record the intent now, so the footer drops the follow-tail key without
	// waiting for the next poll to confirm it.
	p.tailRequested = true
	p.pinned = false
	p.pendingMove = 0
	p.expanded = noSelection
	p.selection = defaultSelection(p.entries)
}

// Entries returns the pane's selectable entries, oldest first.
func (p *TranscriptPane) Entries() []TranscriptEntry { return p.entries }

// Select moves the selection to index i, clamped to the entries. An index is
// valid only until the next SetView, which re-derives the selection by Seq: do
// not hold an index across a poll. A move to a
// different entry pins the selection, so the live tail no longer moves it, and
// collapses the previously expanded entry. A call on an empty pane does
// nothing, and a call that does not move the selection changes no state.
func (p *TranscriptPane) Select(i int) {
	if len(p.entries) == 0 {
		p.selection = noSelection
		p.expanded = noSelection
		return
	}
	i = clampIndex(i, len(p.entries))
	if i == p.selection {
		return
	}
	p.pinned = true
	p.expanded = noSelection
	p.selection = i
}

// MoveSelection moves the selection n entries towards the tail. Negative n
// moves towards the retained start. A move past a window edge asks the scroller
// for the neighbouring events, so the selection is not confined to the window.
func (p *TranscriptPane) MoveSelection(n int) {
	if n == 0 || len(p.entries) == 0 {
		return
	}
	target := p.selection + n
	switch {
	case target < 0:
		p.requestScroll(-target)
	case target > len(p.entries)-1:
		p.requestScroll(-(target - (len(p.entries) - 1)))
	}
	p.Select(target)
}

// requestScroll asks for n events of scrollback (negative n moves towards the
// tail) and records the move for the window that answers. A request the window
// cannot serve is dropped, so an inert key press never pins the selection. Both
// edges guard: no scroller, no newer events at the tail, no older events at the
// retained start. The tailer clamps such a scroll to a no-op, so without the
// guard the pane would leave tail-following on a key press that moves nothing.
func (p *TranscriptPane) requestScroll(n int) {
	if p.scroller == nil || n == 0 {
		return
	}
	if n < 0 {
		if p.view.AtTail {
			return
		}
		p.scroller.ScrollDown(-n)
	} else {
		if p.view.AtStart {
			return
		}
		p.scroller.ScrollUp(n)
	}
	p.pendingMove = -n
	p.pinned = true
}

// ToggleExpand expands the selected tool call, or collapses it when it is
// already expanded. Entries that are not tool calls have nothing to expand.
func (p *TranscriptPane) ToggleExpand() {
	if !p.CanExpand() {
		return
	}
	if p.expanded == p.selection {
		p.expanded = noSelection
		return
	}
	p.expanded = p.selection
}

// Expanded reports that the entry at index i renders expanded.
func (p *TranscriptPane) Expanded(i int) bool { return i != noSelection && p.expanded == i }

// CanExpand reports that the selection is an expandable tool call.
func (p *TranscriptPane) CanExpand() bool {
	e, ok := p.SelectedEntry()
	return ok && e.IsToolCall()
}

// SelectedEntry returns the selected entry. The bool is false on an empty pane.
func (p *TranscriptPane) SelectedEntry() (TranscriptEntry, bool) {
	if p.selection < 0 || p.selection >= len(p.entries) {
		return TranscriptEntry{}, false
	}
	return p.entries[p.selection], true
}

// selectedSeq returns the Seq of the selected entry's own event.
func (p *TranscriptPane) selectedSeq() (int, bool) {
	e, ok := p.SelectedEntry()
	if !ok {
		return 0, false
	}
	return e.Event.Seq, true
}

// defaultSelection selects the newest entry, so a fresh pane follows the tail.
func defaultSelection(entries []TranscriptEntry) int {
	if len(entries) == 0 {
		return noSelection
	}
	return len(entries) - 1
}

// clampIndex holds i inside a list of n entries.
func clampIndex(i, n int) int {
	if i < 0 {
		return 0
	}
	if i > n-1 {
		return n - 1
	}
	return i
}

// indexOfSeq finds the entry whose own event carries seq.
func indexOfSeq(entries []TranscriptEntry, seq int) int {
	for i, e := range entries {
		if e.Event.Seq == seq {
			return i
		}
	}
	return noSelection
}

// buildEntries folds each TOOL_RESULT into the TOOL_CALL that it answers.
// A result whose call the window no longer retains stays its own entry, so
// eviction never hides work that did happen.
func buildEntries(events []TranscriptEvent) []TranscriptEntry {
	entries := make([]TranscriptEntry, 0, len(events))
	callAt := make(map[string]int, len(events))
	for _, e := range events {
		if e.Type == eventToolResult {
			if at, ok := callAt[e.ToolCallID]; ok && e.ToolCallID != "" {
				res := e
				entries[at].Result = &res
				continue
			}
		}
		if e.Type == eventToolCall && e.ToolCallID != "" {
			callAt[e.ToolCallID] = len(entries)
		}
		entries = append(entries, TranscriptEntry{Event: e})
	}
	return entries
}

// TranscriptKeys returns the keys legal while the transcript pane holds
// focus. Derived from the pane's own state, so the footer can never advertise
// an expand on prose. The follow-tail key appears while the window is scrolled
// back, and also while the selection is pinned at the tail: it is then the only
// key that unpins an operator selection. No roster action (cancel, retry,
// answer) appears here: those act on a Worker, not on an event.
func TranscriptKeys(p *TranscriptPane) []KeyBinding {
	keys := []KeyBinding{{Key: "q", Label: "quit"}, {Key: "tab", Label: "roster"}}
	if p == nil {
		return keys
	}
	if p.CanExpand() {
		label := "expand"
		if p.Expanded(p.selection) {
			label = "collapse"
		}
		keys = append(keys, KeyBinding{Key: "enter", Label: label})
	}
	if len(p.entries) > 1 || !p.view.AtTail {
		keys = append(keys, KeyBinding{Key: "k", Label: "up"}, KeyBinding{Key: "j", Label: "down"})
	}
	if p.scroller != nil && !p.tailRequested && (!p.view.AtTail || p.pinned) {
		keys = append(keys, KeyBinding{Key: "G", Label: "follow tail"})
	}
	return keys
}

// RenderTranscript draws the transcript pane: the eviction marker, then one
// line group per entry. A nil pane renders nothing. The detail strip belongs to
// the frame, not the pane.
func RenderTranscript(p *TranscriptPane) string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	if p.view.Evicted {
		b.WriteString(evictionLine(p.view.Dropped))
		b.WriteByte('\n')
	}
	for i, e := range p.entries {
		for _, line := range entryLines(e, i == p.selection, p.Expanded(i)) {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// evictionLine marks history the reader never received. The wording says "not
// retained" and never "truncated", because a TRUNCATION event is the Agent's
// own bounded-transcript marker and means something else entirely.
func evictionLine(dropped int) string {
	if dropped > 0 {
		return fmt.Sprintf("… earlier events not retained (%d dropped)", dropped)
	}
	return "… earlier events not retained"
}

// entryLines renders one entry, collapsed or expanded.
func entryLines(e TranscriptEntry, selected, expanded bool) []string {
	cur := " "
	if selected {
		cur = ">"
	}
	glyph := TranscriptGlyph(e.Event)
	switch e.Event.Type {
	case eventToolCall:
		return toolCallLines(e, cur, glyph, expanded)
	case eventToolResult:
		return withFirstOutput([]string{header(cur, glyph, e.Event.ToolName)}, e.Event.ToolOutput)
	case eventTruncation:
		return []string{header(cur, glyph, truncationText(e.Event.Text))}
	default:
		return []string{header(cur, glyph, firstLine(e.Event.Text))}
	}
}

// toolCallLines renders a tool call: collapsed to its name plus the first
// output line of its result, or expanded to the whole call and result.
func toolCallLines(e TranscriptEntry, cur, glyph string, expanded bool) []string {
	lines := []string{header(cur, glyph, e.Event.ToolName)}
	if !expanded {
		if e.Result != nil {
			lines = withFirstOutput(lines, e.Result.ToolOutput)
		}
		return lines
	}
	lines = append(lines, indentedBlock(e.Event.ToolInput)...)
	if e.Result != nil {
		lines = append(lines, header(" ", TranscriptGlyph(*e.Result), e.Result.ToolName))
		lines = append(lines, indentedBlock(e.Result.ToolOutput)...)
	}
	return lines
}

// withFirstOutput adds the first output line under an entry. A tool that
// returns nothing adds no line, so the render carries no blank row.
func withFirstOutput(lines []string, output string) []string {
	if first := firstLine(output); first != "" {
		return append(lines, indented(first))
	}
	return lines
}

// truncationText labels the Agent's own bounded-transcript marker. The wording
// stays distinct from the reader-side eviction marker.
func truncationText(text string) string {
	if text == "" {
		return "earlier events truncated by the agent"
	}
	return "earlier events truncated by the agent: " + text
}

// transcriptDetailLine renders the selected entry's seq, kind, tool, and
// whether a result has arrived.
func transcriptDetailLine(e TranscriptEntry) string {
	tool := e.Event.ToolName
	if tool == "" {
		tool = "—"
	}
	res := "pending"
	switch {
	case e.Result != nil:
		res = "returned"
	case !e.IsToolCall():
		res = "—"
	}
	return fmt.Sprintf("seq %d | %s | tool %s | result %s", e.Event.Seq, e.Event.Type, tool, res)
}

// header renders one entry's first line: cursor, glyph column, then text.
func header(cur, glyph, text string) string {
	return strings.TrimRight(fmt.Sprintf("%s %s %s", cur, glyph, text), " ")
}

// indented renders one continuation line under an entry's header.
func indented(text string) string {
	if text == "" {
		return "    "
	}
	return "    " + text
}

// indentedBlock renders every line of text as continuation lines.
func indentedBlock(text string) []string {
	if text == "" {
		return nil
	}
	raw := strings.Split(strings.TrimRight(text, "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		out = append(out, indented(l))
	}
	return out
}

// firstLine returns text up to its first newline.
func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}

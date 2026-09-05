package tui

// pane.go: transcript pane rendering plus collapse, expand, and selection
// state.

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
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
		return "▸"
	case eventToolResult:
		return "└"
	case eventTruncation:
		return "░"
	default:
		return " "
	}
}

// noSelection marks an empty pane, which has no selectable entry.
const noSelection = -1

// eventKey identifies one transcript event across a multi-attempt history.
// Every AgentRun restarts Seq at 0, so the run must be part of the key.
type eventKey struct {
	runID int64
	seq   int
}

// keyOf builds the scrollback anchor for one event. The tailer holds its
// scrolled-back position with it.
func keyOf(e TranscriptEvent) eventKey { return eventKey{runID: e.AgentRunID, seq: e.Seq} }

// TranscriptEntry is one selectable timeline item. A tool call folds its
// paired result into the same entry, so collapse, expand, and selection all
// act on the call and its result together.
type TranscriptEntry struct {
	Event TranscriptEvent
	// Result is the paired TOOL_RESULT, when the call has returned and the
	// window retains it.
	Result *TranscriptEvent
	// Gate marks a synthetic quality-gate row. Event stays zero on such an
	// entry: a gate is no TranscriptEvent (see gate.go).
	Gate *GateRow
}

// IsToolCall reports that the entry is a tool call.
func (e TranscriptEntry) IsToolCall() bool { return e.Gate == nil && e.Event.Type == eventToolCall }

// IsGate reports that the entry is a synthetic quality-gate row.
func (e TranscriptEntry) IsGate() bool { return e.Gate != nil }

// key identifies an entry across polls. Every AgentRun restarts Seq at 0, so an
// event key carries its run as well. A gate row carries no Seq, so the two kinds
// share one string namespace.
func (e TranscriptEntry) key() string {
	if e.Gate != nil {
		return e.Gate.key
	}
	return "run:" + strconv.FormatInt(e.Event.AgentRunID, 10) + ":seq:" + strconv.Itoa(e.Event.Seq)
}

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
	// gates are the quality-gate rows the pane appends after the event window.
	gates []GateRow
	// eventCount is the number of leading entries that come from the event
	// window. The trailing gate rows are no part of the window, so a move past
	// this edge must still ask the scroller for newer events.
	eventCount int

	// scroller moves the window anchor. Nil means the pane cannot follow the
	// tail, and the footer then hides the follow-tail key.
	scroller TranscriptScroller

	// style colours a rendered header by entry kind. The zero value applies no
	// colour, which every headless render test relies on; the live view sets
	// forge's real scheme.
	style Style

	// width is the terminal width in cells RenderTranscript wraps a line to.
	// Zero applies no wrap: the runtime has not yet reported a terminal size,
	// which every headless render test relies on.
	width int

	// height is the row budget RenderTranscript clamps its output to. Zero
	// applies no clamp: the runtime has not yet reported a terminal size,
	// which every headless render test relies on.
	height int

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

// SetStyle sets the colour scheme a rendered header applies by entry kind.
// The zero Style applies no colour.
func (p *TranscriptPane) SetStyle(s Style) { p.style = s }

// SetWidth sets the terminal width RenderTranscript wraps a line to, so a
// line the terminal would itself wrap renders as the several rows it draws.
// A width of zero or less applies no wrap.
func (p *TranscriptPane) SetWidth(w int) { p.width = w }

// SetHeight sets the row budget RenderTranscript clamps its output to, so an
// expanded entry or a tall window cannot push the detail strip and the footer
// off screen. A height of zero or less applies no clamp: the runtime has not
// yet reported a terminal size.
func (p *TranscriptPane) SetHeight(h int) { p.height = h }

// SetGates replaces the quality-gate rows the pane appends after the event
// window. Gate runs follow the Agent's own work, so they render last. The call
// rebuilds the entries through rebuild, the pane's one rebuild path, so
// selection and expansion follow the same key-based rules a poll obeys. It
// leaves pendingMove and tailRequested alone: both wait for the poll that
// answers them.
//
// TranscriptFeed calls this on each poll with the store's runs for the Issue.
func (p *TranscriptPane) SetGates(rows []GateRow) {
	// Copy, so a caller that reuses one buffer across polls cannot change the
	// rendered rows behind a rebuild.
	p.gates = append([]GateRow(nil), rows...)
	p.rebuild(0)
}

// SetView replaces the visible window and re-appends the gate rows. A pinned
// selection holds its entry by key where the new window still retains it, so a
// poll that appends events does not move the operator's selection. An unpinned
// selection follows the tail while the window is at the tail. Expansion
// survives only where the selection holds the same entry.
func (p *TranscriptPane) SetView(vm TranscriptViewModel) {
	pending := p.pendingMove
	p.pendingMove = 0
	p.tailRequested = false
	p.view = vm
	p.rebuild(pending)
}

// rebuild derives the entries from the current window and gate rows, then
// re-derives the selection and the expansion by key. Every entry change goes
// through here, so the two callers cannot diverge. pending applies a selection
// move that an earlier window could not satisfy.
func (p *TranscriptPane) rebuild(pending int) {
	prevEntry, hadSelection := p.SelectedEntry()
	prevKey := prevEntry.key()
	wasExpanded := p.expanded != noSelection

	events := buildEntries(p.view.Events)
	p.eventCount = len(events)
	p.entries = append(events, gateEntries(p.gates)...)

	idx := p.defaultSelection()
	if hadSelection && (p.pinned || !p.view.AtTail) {
		idx = indexOfKey(p.entries, prevKey)
		if idx == noSelection {
			idx = p.anchorLostSelection(prevEntry)
		}
	}
	if pending != 0 && idx != noSelection {
		idx = clampIndex(idx+pending, len(p.entries))
	}

	p.selection = idx
	p.expanded = noSelection
	if wasExpanded {
		if e, ok := p.SelectedEntry(); ok && e.key() == prevKey {
			p.expanded = idx
		}
	}
}

// anchorLostSelection handles a pinned entry that the advancing tail pushed out
// of the window. A poll can append several events at once, so the entry can
// fall behind by more than one: this asks for exactly the shortfall between
// the entry's Seq and the window's new leading Seq, which recovers the entry
// in this one pass instead of one event per poll. Where the shortfall cannot
// be measured — lost carries no event, or it belongs to a different run than
// the window's leading entry — it falls back to one event of scrollback.
func (p *TranscriptPane) anchorLostSelection(lost TranscriptEntry) int {
	if p.scroller == nil || len(p.entries) == 0 || p.view.AtStart {
		return p.defaultSelection()
	}
	p.scroller.ScrollUp(lostSelectionShortfall(lost, p.view))
	return 0
}

// lostSelectionShortfall returns how many events behind the window's leading
// entry the lost entry fell. It falls back to one event where lost carries no
// event (a gate row) or the window holds no event of the same run.
func lostSelectionShortfall(lost TranscriptEntry, vm TranscriptViewModel) int {
	if lost.IsGate() || len(vm.Events) == 0 || vm.FirstRunID != lost.Event.AgentRunID {
		return 1
	}
	if shortfall := vm.FirstSeq - lost.Event.Seq; shortfall > 0 {
		return shortfall
	}
	return 1
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
	p.selection = p.defaultSelection()
}

// Entries returns the pane's selectable entries, oldest first.
func (p *TranscriptPane) Entries() []TranscriptEntry { return p.entries }

// Select moves the selection to index i, clamped to the entries. An index is
// valid only until the next SetView, which re-derives the selection by key: do
// not hold an index across a poll. A move to a different entry pins the
// selection, so the live tail no longer moves it, and collapses the previously
// expanded entry. A call on an empty pane does nothing, and a call that does
// not move the selection changes no state.
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
// The tail edge is the last event entry and not the last entry: the trailing
// gate rows must not hide the edge, or the window never advances by key.
func (p *TranscriptPane) MoveSelection(n int) {
	if n == 0 || len(p.entries) == 0 {
		return
	}
	target := p.selection + n
	tailEdge := p.eventEdge()
	switch {
	case target < 0:
		p.requestScroll(-target)
	case tailEdge != noSelection && target > tailEdge:
		p.requestScroll(-(target - tailEdge))
		// A served request holds the selection at the event edge, so the move
		// the new window applies starts from the event and not from a gate row.
		if p.pendingMove != 0 {
			target = tailEdge
		}
	}
	p.Select(target)
}

// pageSize returns one page of scrollback: the current window's event count,
// or 1 while the window holds none.
func (p *TranscriptPane) pageSize() int {
	if n := len(p.view.Events); n > 0 {
		return n
	}
	return 1
}

// PageUp moves the window one page towards the retained start and anchors
// the selection to the window's own top entry, so an operator reaches
// scrollback in a few key presses instead of one event at a time. An inert
// request (already at the retained start) changes no state.
func (p *TranscriptPane) PageUp() {
	if p.requestScroll(p.pageSize()) {
		p.Select(0)
	}
}

// PageDown moves the window one page towards the tail and anchors the
// selection to the window's own bottom entry. An inert request (already at
// the tail) changes no state.
func (p *TranscriptPane) PageDown() {
	if p.requestScroll(-p.pageSize()) {
		p.Select(p.eventEdge())
	}
}

// requestScroll asks for n events of scrollback (negative n moves towards the
// tail) and records the move for the window that answers, reporting whether it
// asked. A request the window cannot serve is dropped, so an inert key press
// never pins the selection. Both edges guard: no scroller, no newer events at
// the tail, no older events at the retained start. The tailer clamps such a
// scroll to a no-op, so without the guard the pane would leave tail-following
// on a key press that moves nothing.
func (p *TranscriptPane) requestScroll(n int) bool {
	if p.scroller == nil || n == 0 {
		return false
	}
	if n < 0 {
		if p.view.AtTail {
			return false
		}
		p.scroller.ScrollDown(-n)
	} else {
		if p.view.AtStart {
			return false
		}
		p.scroller.ScrollUp(n)
	}
	p.pendingMove = -n
	p.pinned = true
	return true
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

// CanExpand reports that the selection expands: a tool call or a gate row.
func (p *TranscriptPane) CanExpand() bool {
	e, ok := p.SelectedEntry()
	return ok && (e.IsToolCall() || e.IsGate())
}

// SelectedEntry returns the selected entry. The bool is false on an empty pane.
func (p *TranscriptPane) SelectedEntry() (TranscriptEntry, bool) {
	if p.selection < 0 || p.selection >= len(p.entries) {
		return TranscriptEntry{}, false
	}
	return p.entries[p.selection], true
}

// eventEdge returns the index of the last event entry, or noSelection where the
// window holds no event. rebuild appends the gate rows after the event entries,
// so eventCount is the whole layout rule and this is the one place that turns it
// into an index.
func (p *TranscriptPane) eventEdge() int {
	if p.eventCount == 0 {
		return noSelection
	}
	return p.eventCount - 1
}

// defaultSelection selects the newest event entry, so a fresh pane follows the
// tail. An unpinned selection must hold the live Agent event and must not stick
// to the newest gate row. A pane that holds gate rows alone selects the last of
// them.
func (p *TranscriptPane) defaultSelection() int {
	if edge := p.eventEdge(); edge != noSelection {
		return edge
	}
	if len(p.entries) == 0 {
		return noSelection
	}
	return len(p.entries) - 1
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

// indexOfKey finds the entry that carries key.
func indexOfKey(entries []TranscriptEntry, key string) int {
	for i, e := range entries {
		if e.key() == key {
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
	type callRef struct {
		runID  int64
		callID string
	}
	callAt := make(map[callRef]int, len(events))
	for _, e := range events {
		ref := callRef{runID: e.AgentRunID, callID: e.ToolCallID}
		if e.Type == eventToolResult {
			if at, ok := callAt[ref]; ok && e.ToolCallID != "" {
				res := e
				entries[at].Result = &res
				continue
			}
		}
		if e.Type == eventToolCall && e.ToolCallID != "" {
			callAt[ref] = len(entries)
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
	if p.scroller != nil && (!p.view.AtStart || !p.view.AtTail) {
		keys = append(keys, KeyBinding{Key: "pgup", Label: "page up"}, KeyBinding{Key: "pgdown", Label: "page down"})
	}
	if p.scroller != nil && !p.tailRequested && (!p.view.AtTail || p.pinned) {
		keys = append(keys, KeyBinding{Key: "G", Label: "follow tail"})
	}
	return keys
}

// RenderTranscript draws the transcript pane: the eviction marker, then one
// line group per entry. A multi-attempt history carries an "attempt N" divider
// above the first entry of each run, which includes the first entry of the
// window. A single-attempt history carries no divider. A nil pane renders
// nothing. The detail strip belongs to the frame, not the pane.
//
// A height budget set through SetHeight clamps the output to that many rows,
// so an expansion or a tall window that would otherwise outgrow the terminal
// cannot push the detail strip and the footer off screen. The clamp keeps
// whole groups (a divider, the eviction marker, one entry's lines) together.
// An unpinned selection follows the live tail, so the clamp anchors on the
// newest group and matches a plain tail clip; a pinned selection anchors on
// the selected entry's own group instead, so scrolling back to inspect an old
// entry never scrolls it back off screen behind a tall window. Either way the
// window then grows outward towards the tail first and then towards the
// retained start. A budget of zero or less clamps nothing.
func RenderTranscript(p *TranscriptPane) string {
	if p == nil {
		return ""
	}
	groups, selGroup := transcriptGroups(p)
	if p.height > 0 {
		anchor := -1
		if p.pinned {
			anchor = selGroup
		}
		groups = clampTranscriptGroups(groups, anchor, p.height)
	}
	var b strings.Builder
	for _, g := range groups {
		for _, row := range g {
			b.WriteString(row)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// transcriptGroups renders p into line groups: one group per atomic unit (the
// eviction marker, an attempt divider, one entry). selGroup is the index into
// groups of the selected entry's own group, or -1 when nothing is selected.
// clampTranscriptGroups anchors its budget on selGroup, so this split is the
// one place that decides what a "group" is for that purpose.
func transcriptGroups(p *TranscriptPane) (groups [][]string, selGroup int) {
	selGroup = -1
	wrapped := func(line string) []string { return wrapWidth(line, p.width) }
	if p.view.Evicted {
		groups = append(groups, wrapped(p.style.Truncation.Render(evictionLine(p.view.Dropped))))
	}
	attempts := attemptNumbers(p.view.RunOrder)
	divide := len(p.view.RunOrder) > 1
	prevRun := int64(0)
	for i, e := range p.entries {
		// A gate row belongs to no attempt, so it never opens one. An unknown run
		// carries no attempt number and draws no divider rather than a wrong one.
		if divide && !e.IsGate() && (i == 0 || e.Event.AgentRunID != prevRun) {
			if n, ok := attempts[e.Event.AgentRunID]; ok {
				groups = append(groups, wrapped(p.style.Truncation.Render(fmt.Sprintf("── attempt %d ──", n))))
			}
		}
		if !e.IsGate() {
			prevRun = e.Event.AgentRunID
		}
		var entry []string
		for _, line := range entryLines(e, i == p.selection, p.Expanded(i), p.style) {
			entry = append(entry, wrapped(line)...)
		}
		if i == p.selection {
			selGroup = len(groups)
		}
		groups = append(groups, entry)
	}
	return groups, selGroup
}

// clampTranscriptGroups keeps groups within budget rows, anchored on the
// group at anchor (the last group when anchor is -1, so an unselected pane
// still clamps towards its newest content). It grows the kept window outward
// from the anchor towards the tail first, then towards the retained start,
// stopping in a direction once the next group there would not fit, so the
// window stays contiguous and never leaves a gap. A single group larger than
// budget clamps to its own last budget rows, the same tail bias as the
// window as a whole; the row wrapWidth splits a styled line across can carry
// no opening escape of its own (only the split's first physical row does), so
// a leading reset guards the kept rows against inheriting an open style from
// the row dropped above them.
func clampTranscriptGroups(groups [][]string, anchor, budget int) [][]string {
	if len(groups) == 0 || budget <= 0 {
		return groups
	}
	if anchor < 0 || anchor >= len(groups) {
		anchor = len(groups) - 1
	}
	if len(groups[anchor]) >= budget {
		if extra := len(groups[anchor]) - budget; extra > 0 {
			kept := append([]string(nil), groups[anchor][extra:]...)
			kept[0] = "\x1b[0m" + kept[0]
			return [][]string{kept}
		}
		return [][]string{groups[anchor]}
	}
	lo, hi := anchor, anchor
	used := len(groups[anchor])
	canTail, canHead := true, true
	for canTail || canHead {
		progressed := false
		if canTail {
			if hi+1 < len(groups) && used+len(groups[hi+1]) <= budget {
				hi++
				used += len(groups[hi])
				progressed = true
			} else {
				canTail = false
			}
		}
		if canHead && used < budget {
			if lo-1 >= 0 && used+len(groups[lo-1]) <= budget {
				lo--
				used += len(groups[lo])
				progressed = true
			} else {
				canHead = false
			}
		}
		if !progressed {
			break
		}
	}
	return groups[lo : hi+1]
}

// attemptNumbers maps each AgentRun to its 1-based attempt number. The order
// is run insertion order, which the tailer keeps, so a retry always numbers
// above the attempt it follows.
func attemptNumbers(runOrder []int64) map[int64]int {
	attempts := make(map[int64]int, len(runOrder))
	for i, id := range runOrder {
		attempts[id] = i + 1
	}
	return attempts
}

// evictionLine marks history the reader never received. The wording says "not
// retained" and never "truncated", because a TRUNCATION event is the Agent's
// own bounded-transcript marker and means something else entirely.
func evictionLine(dropped int) string {
	if dropped > 0 {
		return fmt.Sprintf("░ earlier events not retained (%d dropped)", dropped)
	}
	return "░ earlier events not retained"
}

// entryLines renders one entry, collapsed or expanded, through style: the
// pane's colour scheme by entry kind. The zero Style applies no colour.
func entryLines(e TranscriptEntry, selected, expanded bool, style Style) []string {
	cur := " "
	if selected {
		cur = ">"
	}
	if e.Gate != nil {
		return gateLines(*e.Gate, cur, expanded, style)
	}
	glyph := TranscriptGlyph(e.Event)
	switch e.Event.Type {
	case eventToolCall:
		return toolCallLines(e, cur, glyph, expanded, style)
	case eventToolResult:
		return withFirstOutput([]string{header(headerParts{cursor: cur, glyph: glyph, axis: e.Event.Subagent, text: e.Event.ToolName}, style.Tool, style.Axis)}, e.Event.ToolOutput)
	case eventTruncation:
		return []string{header(headerParts{cursor: cur, glyph: glyph, axis: e.Event.Subagent, text: truncationText(e.Event.Text)}, style.Truncation, style.Axis)}
	default:
		return []string{header(headerParts{cursor: cur, glyph: glyph, axis: e.Event.Subagent, text: firstLine(e.Event.Text)}, style.Message, style.Axis)}
	}
}

// toolCallLines renders a tool call: collapsed to its name plus the first
// output line of its result, or expanded to the whole call and result. style
// colours both the call's and the result's header through style.Tool, with the
// axis label apart in style.Axis.
func toolCallLines(e TranscriptEntry, cur, glyph string, expanded bool, style Style) []string {
	lines := []string{header(headerParts{cursor: cur, glyph: glyph, axis: e.Event.Subagent, text: e.Event.ToolName}, style.Tool, style.Axis)}
	if !expanded {
		if e.Result != nil {
			lines = withFirstOutput(lines, e.Result.ToolOutput)
		}
		return lines
	}
	lines = append(lines, indentedBlock(e.Event.ToolInput)...)
	if e.Result != nil {
		lines = append(lines, header(headerParts{cursor: " ", glyph: TranscriptGlyph(*e.Result), axis: e.Result.Subagent, text: e.Result.ToolName}, style.Tool, style.Axis))
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
	if e.Gate != nil {
		return gateDetailLine(*e.Gate)
	}
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
	axis := e.Event.Subagent
	if axis == "" {
		axis = "—"
	}
	return fmt.Sprintf("seq %d | %s | axis %s | tool %s | result %s",
		e.Event.Seq, e.Event.Type, axis, tool, res)
}

// headerParts names one entry header's columns. The fields carry names rather
// than a run of positional strings, so a swapped axis and text cannot compile.
type headerParts struct {
	cursor string
	glyph  string
	// axis labels the review axis (bugs, quality, docs) that produced the entry.
	// Empty for the implementation Agent, which has no subagent.
	axis string
	text string
}

// header renders one entry's first line: cursor, glyph column, inline axis
// label, then text. textStyle colours the cursor, glyph, and text; axisStyle
// colours the [axis] label apart from them, so the review axis reads as its own
// hue. The three review axes interleave in the one pane, so every entry kind
// carries its label from here: the label is the only separation. A zero
// textStyle and a zero axisStyle both apply no colour, so a headless render
// stays plain and byte-identical.
func header(h headerParts, textStyle, axisStyle lipgloss.Style) string {
	left := fmt.Sprintf("%s %s", h.cursor, h.glyph)
	if h.axis == "" {
		return textStyle.Render(strings.TrimRight(left+" "+h.text, " "))
	}
	line := textStyle.Render(left) + " " + axisStyle.Render("["+h.axis+"]")
	if h.text != "" {
		line += " " + textStyle.Render(h.text)
	}
	return line
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

// lastLine returns the final line of text that holds more than whitespace. A
// gate log can end in blank or padded lines, which must never render as an empty
// preview row.
func lastLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimRight(lines[i], " \t\r"); strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}

// firstLine returns text up to its first newline.
func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}

package tui

// layout.go renders the execute TUI's frame: an execution list on top, then
// three body panes side by side (agents on the left, live output in the
// centre, the diff on the right), then the detail strip and the footer. Like
// frame.go it is a pure function from the view-model to a string, so the
// whole layout is testable headless.

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Teagan42/forge/internal/domain"
)

const (
	// defaultFrameWidth is the width Render lays out at before the runtime
	// reports a terminal size.
	defaultFrameWidth = 100
	// maxExecutionRows is how many Workers the execution list shows at once.
	// A longer list scrolls to keep the selection visible.
	maxExecutionRows = 3
	// agentsPaneWidth is the agents pane's full width, borders included.
	agentsPaneWidth = 26
	// diffPaneWidth is the diff pane's full width, borders included.
	diffPaneWidth = 36
	// minTranscriptWidth is the least width the output pane keeps. A
	// narrower terminal shrinks the side panes instead.
	minTranscriptWidth = 30
	// shortIDLen is how many characters of the Execution ID the header shows.
	shortIDLen = 8
)

// Pane titles, shared by the renderer and the tests that look for them.
const (
	titleExecutions = "Executions"
	titleAgents     = "Agents"
	titleOutput     = "Output"
	titleDiff       = "Diff"
)

// Render draws the whole frame: the execution list, the notices, the three
// body panes, the detail strip for the focused pane's selection, and a footer
// of the keys legal right now. The output pane is clipped to the rows
// vm.Height leaves, so the frame never draws past the terminal bottom. Pure
// and headless.
func Render(vm ViewModel) string {
	width := frameWidth(vm)
	top := topLines(vm, width)
	bottom := bottomLines(vm, width)
	body := bodyLines(vm, width, bodyRows(vm, top, bottom))
	var b strings.Builder
	for _, l := range [][]string{top, body, bottom} {
		for _, line := range l {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// TranscriptRows returns the rows vm.Height leaves the output pane's body,
// after the execution list, the notices, the body borders, the detail strip,
// and the footer. Render clips to it and the live view budgets the tailer's
// event window against it, so one place owns the arithmetic. Zero means
// vm.Height is unset and nothing is clipped. One is the floor.
func TranscriptRows(vm ViewModel) int {
	width := frameWidth(vm)
	return bodyRows(vm, topLines(vm, width), bottomLines(vm, width))
}

// TranscriptWidth returns the cells the output pane's body spans: the frame
// width less the agents pane, the diff pane while open, and the borders. The
// live view wraps transcript lines to it, so a long line never breaks the
// frame.
func TranscriptWidth(vm ViewModel) int {
	_, transcriptW, _ := paneWidths(vm, frameWidth(vm))
	return transcriptW - 2
}

// frameWidth returns the width Render lays out at.
func frameWidth(vm ViewModel) int {
	if vm.Width > 0 {
		return vm.Width
	}
	return defaultFrameWidth
}

// bodyRows returns the body panes' inner row budget from the terminal height
// once top and bottom are drawn. Zero means height is unset and nothing is
// clipped. One is the floor.
func bodyRows(vm ViewModel, top, bottom []string) int {
	if vm.Height <= 0 {
		return 0
	}
	if rows := vm.Height - len(top) - len(bottom) - 2; rows > 1 {
		return rows
	}
	return 1
}

// topLines draws the execution list and the notices under it.
func topLines(vm ViewModel, width int) []string {
	lines := executionPanel(vm, width)
	// A notice also carries a failed poll pass, which holds the last good rows.
	// It must render with those rows, or the failure is invisible.
	for _, notice := range notices(vm) {
		lines = append(lines, truncLine(vm.Style.Notice.Render(notice), width))
	}
	return lines
}

// notices lists the frame's notices in a fixed order: the roster, the
// transcript, a lagging transcript, then the last action.
func notices(vm ViewModel) []string {
	var out []string
	if vm.Notice != "" {
		out = append(out, vm.Notice)
	}
	if vm.TranscriptNotice != "" {
		out = append(out, vm.TranscriptNotice)
	}
	if DeriveTranscriptLag(vm.TranscriptLagAge, vm.PollInterval) {
		out = append(out, transcriptLagLine(vm.TranscriptLagAge))
	}
	if vm.ActionNotice != "" {
		out = append(out, vm.ActionNotice)
	}
	return out
}

// bottomLines draws the detail strip for the focused pane's selection, when
// it has one, then the footer.
func bottomLines(vm ViewModel, width int) []string {
	var lines []string
	if strip, ok := stripLine(vm); ok {
		lines = append(lines, truncLine(strip, width))
	}
	return append(lines, truncLine(footerLine(frameKeys(vm), vm.Style), width))
}

// showAgents reports whether the frame draws the agents pane: only with a
// selected Worker, whose agents it lists.
func showAgents(vm ViewModel) bool {
	_, ok := selectedWorker(vm)
	return ok
}

// paneWidths splits width between the agents pane, the output pane, and the
// diff pane, borders included. A pane not shown gets zero. A narrow terminal
// shrinks the side panes so the output pane keeps minTranscriptWidth.
func paneWidths(vm ViewModel, width int) (agentsW, transcriptW, diffW int) {
	if showAgents(vm) {
		agentsW = agentsPaneWidth
	}
	if vm.DiffOpen {
		diffW = diffPaneWidth
	}
	if width-agentsW-diffW < minTranscriptWidth {
		if agentsW > 0 {
			agentsW = width / 4
		}
		if diffW > 0 {
			diffW = width / 3
		}
	}
	return agentsW, width - agentsW - diffW, diffW
}

// bodyLines draws the three body panes side by side, each rows tall inside
// its border. A zero rows budget sizes every pane to the tallest content. No
// Worker and no transcript draws no body at all.
func bodyLines(vm ViewModel, width, rows int) []string {
	if !showAgents(vm) && vm.Transcript == nil {
		return nil
	}
	agentsW, transcriptW, diffW := paneWidths(vm, width)
	agents := agentLines(vm, agentsW-2)
	transcript := transcriptLines(vm, transcriptW-2, rows)
	diff := diffLines(vm, diffW-2, rows)
	if rows <= 0 {
		rows = max(len(agents), len(transcript), len(diff), 1)
	}
	var panels [][]string
	if agentsW > 0 {
		panels = append(panels, panel(titleAgents, agents, agentsW, rows, vm.Focus == PaneAgents, vm.Style))
	}
	panels = append(panels, panel(titleOutput, transcript, transcriptW, rows, vm.Focus == PaneTranscript, vm.Style))
	if diffW > 0 {
		panels = append(panels, panel(diffTitle(vm), diff, diffW, rows, vm.Focus == PaneDiff, vm.Style))
	}
	out := make([]string, rows+2)
	for i := range out {
		var b strings.Builder
		for _, p := range panels {
			b.WriteString(p[i])
		}
		out[i] = b.String()
	}
	return out
}

// executionPanel draws the execution list: a column header, then at most
// maxExecutionRows Workers, scrolled to keep the selection visible. The
// header names the Execution and, when the list scrolls, the visible range.
func executionPanel(vm ViewModel, width int) []string {
	inner := width - 2
	title := titleExecutions
	if vm.ExecutionID != "" {
		title += " · " + shortID(vm.ExecutionID)
	}
	start, end := executionWindow(vm.Selection, len(vm.Workers))
	if len(vm.Workers) > maxExecutionRows {
		title += fmt.Sprintf(" (%d-%d of %d)", start+1, end, len(vm.Workers))
	}
	cols := newExecutionColumns(inner)
	lines := []string{vm.Style.Header.Render(cols.header())}
	for i := start; i < end; i++ {
		lines = append(lines, cols.row(vm.Workers[i], i == vm.Selection, vm.Style))
	}
	return panel(title, lines, width, len(lines), vm.Focus == PaneRoster, vm.Style)
}

// executionWindow returns the [start, end) rows the list shows so that
// selection stays visible: the list scrolls only once the selection passes
// the last visible row.
func executionWindow(selection, n int) (start, end int) {
	if n <= maxExecutionRows {
		return 0, n
	}
	start = 0
	if selection >= maxExecutionRows {
		start = selection - maxExecutionRows + 1
	}
	if start > n-maxExecutionRows {
		start = n - maxExecutionRows
	}
	return start, start + maxExecutionRows
}

// shortID returns the first shortIDLen characters of an Execution ID.
func shortID(id string) string {
	if len(id) > shortIDLen {
		return id[:shortIDLen]
	}
	return id
}

// executionColumns holds the execution list's column widths for one frame
// width. The fixed columns (glyphs, id, status, elapsed, agents) keep their
// width; the name and the latest output share what remains.
type executionColumns struct {
	name, latest int
}

// Fixed column widths of the execution list.
const (
	colGlyphs  = 6 // cursor, attention, liveness, and their separators
	colID      = 10
	colStatus  = 13
	colElapsed = 8
	colAgents  = 6
	colFixed   = colGlyphs + colID + 1 + 1 + colStatus + 1 + colElapsed + 1 + colAgents + 1
	colNameMax = 24
	colNameMin = 10
)

// newExecutionColumns sizes the flexible columns for an inner width.
func newExecutionColumns(inner int) executionColumns {
	name := inner - colFixed - 16
	if name > colNameMax {
		name = colNameMax
	}
	if name < colNameMin {
		name = colNameMin
	}
	latest := inner - colFixed - name
	if latest < 0 {
		latest = 0
	}
	return executionColumns{name: name, latest: latest}
}

// header renders the column header row.
func (c executionColumns) header() string {
	return fmt.Sprintf("%-*s%-*s %-*s %-*s %-*s %-*s %s",
		colGlyphs, "", colID, "ID", c.name, "NAME", colStatus, "STATUS", colElapsed, "ELAPSED", colAgents, "AGENTS", "LATEST OUTPUT")
}

// row renders one Worker: cursor, attention and liveness glyphs, id, name,
// state, elapsed, agent count, and the newest output line. The state carries
// its colour; the selected row carries the selection style.
func (c executionColumns) row(row WorkerRow, selected bool, style Style) string {
	cur := " "
	if selected {
		cur = ">"
	}
	att := AttentionGlyph(DeriveAttention(row.State, row.Tool))
	live := LivenessGlyph(DeriveLiveness(row.HasHeartbeat, row.HeartbeatAge))
	state := stateStyle(style, row.State).Render(pad(string(row.State), colStatus))
	line := fmt.Sprintf("%s %s %s %s %s %s %s %s %s",
		cur, att, live,
		pad(row.IssueID, colID),
		pad(row.Title, c.name),
		state,
		pad(formatDuration(row.Elapsed), colElapsed),
		pad(strconv.Itoa(row.AgentCount()), colAgents),
		pad(row.LatestOutput(), c.latest))
	if selected {
		return style.Selection.Render(line)
	}
	return line
}

// stateStyle picks the colour for a Worker state by its coarse group: a
// working state reads as running, done as passed, failed as failed, a parked
// or waiting state as a warning, and a pending state as muted.
func stateStyle(style Style, state domain.IssueState) lipgloss.Style {
	switch state.Group() {
	case domain.GroupWorking:
		return style.Running
	case domain.GroupDone:
		return style.Passed
	case domain.GroupFailed:
		return style.Failed
	case domain.GroupBlocked, domain.GroupWaiting:
		return style.Warning
	default:
		return style.Muted
	}
}

// agentLines draws the agents pane's rows: the cursor, the agent label, and
// its event count. The selected agent carries the selection style.
func agentLines(vm ViewModel, inner int) []string {
	row, ok := selectedWorker(vm)
	if !ok {
		return nil
	}
	const countW = 5
	labelW := inner - 2 - 1 - countW
	if labelW < 1 {
		labelW = 1
	}
	lines := make([]string, 0, len(row.Agents))
	for i, a := range row.Agents {
		cur := " "
		if i == vm.AgentSelection {
			cur = ">"
		}
		count := ""
		if a.Events > 0 {
			count = strconv.Itoa(a.Events)
		}
		line := fmt.Sprintf("%s %s %*s", cur, pad(a.Label, labelW), countW, count)
		if i == vm.AgentSelection {
			line = vm.Style.Selection.Render(line)
		}
		lines = append(lines, line)
	}
	return lines
}

// transcriptLines draws the output pane's body: the transcript clipped to
// rows and fitted to inner. A nil pane draws nothing.
func transcriptLines(vm ViewModel, inner, rows int) []string {
	lines := clipTranscript(vm.Transcript, rows)
	for i, l := range lines {
		lines[i] = fitLine(l, inner)
	}
	return lines
}

// diffTitle names the diff pane with its totals once loaded.
func diffTitle(vm ViewModel) string {
	if vm.Diff == nil {
		return titleDiff
	}
	return fmt.Sprintf("%s +%d -%d", titleDiff, vm.Diff.Additions, vm.Diff.Deletions)
}

// diffLines draws the diff pane's body: one row per changed file with its
// additions and deletions, scrolled from vm.DiffScroll. A missing summary
// says the read is in flight; an empty one says the diff changed no file.
func diffLines(vm ViewModel, inner, rows int) []string {
	if !vm.DiffOpen {
		return nil
	}
	if vm.Diff == nil {
		return []string{vm.Style.Muted.Render("loading diff…")}
	}
	if len(vm.Diff.Files) == 0 {
		return []string{vm.Style.Muted.Render("no changed files")}
	}
	files := vm.Diff.Files
	if vm.DiffScroll > 0 && vm.DiffScroll < len(files) {
		files = files[vm.DiffScroll:]
	}
	if rows > 0 && len(files) > rows {
		files = files[:rows]
	}
	lines := make([]string, 0, len(files))
	for _, f := range files {
		adds := vm.Style.Added.Render("+" + strconv.Itoa(f.Additions))
		dels := vm.Style.Removed.Render("-" + strconv.Itoa(f.Deletions))
		counts := adds + " " + dels
		pathW := inner - lipgloss.Width(counts) - 1
		if pathW < 1 {
			pathW = 1
		}
		lines = append(lines, padLeftTrunc(f.Path, pathW)+" "+counts)
	}
	return lines
}

// stripLine picks the detail strip for the focused pane: the diff's totals,
// the selected agent, the transcript selection's own strip, or the selected
// Worker's.
func stripLine(vm ViewModel) (string, bool) {
	row, ok := selectedWorker(vm)
	switch vm.Focus {
	case PaneDiff:
		if vm.DiffOpen && vm.Diff != nil && ok {
			return fmt.Sprintf("diff %s | %d files | +%d -%d", row.IssueID, len(vm.Diff.Files), vm.Diff.Additions, vm.Diff.Deletions), true
		}
	case PaneAgents:
		if a, ok := selectedAgent(vm); ok {
			return agentDetailLine(a), true
		}
	case PaneTranscript:
		if vm.Transcript != nil {
			if e, ok := vm.Transcript.SelectedEntry(); ok {
				return transcriptDetailLine(e), true
			}
			return "", false
		}
	}
	if ok {
		return detailLine(row), true
	}
	return "", false
}

// agentDetailLine renders the selected agent's label, event count, and
// newest output.
func agentDetailLine(a AgentRow) string {
	last := a.Latest
	if last == "" {
		last = "—"
	}
	return fmt.Sprintf("%s | events %d | last %s", a.Label, a.Events, last)
}

// selectedAgent returns the selected Worker's selected agent row. The bool is
// false with no Worker or an out-of-range agent selection.
func selectedAgent(vm ViewModel) (AgentRow, bool) {
	row, ok := selectedWorker(vm)
	if !ok || vm.AgentSelection < 0 || vm.AgentSelection >= len(row.Agents) {
		return AgentRow{}, false
	}
	return row.Agents[vm.AgentSelection], true
}

// frameKeys picks the footer's keys for the focused pane. The roster leads
// with the selected Worker's legal actions; every pane offers pane
// navigation and the diff toggle where the store holds a diff.
func frameKeys(vm ViewModel) []KeyBinding {
	row, ok := selectedWorker(vm)
	if !ok && vm.Transcript == nil {
		return []KeyBinding{{Key: "q", Label: "quit"}}
	}
	var keys []KeyBinding
	switch vm.Focus {
	case PaneTranscript:
		keys = relabelTab(TranscriptKeys(vm.Transcript))
	case PaneAgents:
		keys = []KeyBinding{{Key: "q", Label: "quit"}}
		if len(row.Agents) > 1 {
			keys = append(keys, KeyBinding{Key: "j/k", Label: "switch agent"})
		}
		if vm.Transcript != nil {
			keys = append(keys, KeyBinding{Key: "enter", Label: "inspect"})
		}
		keys = append(keys, KeyBinding{Key: "tab", Label: "next pane"})
	case PaneDiff:
		keys = []KeyBinding{{Key: "q", Label: "quit"}}
		if vm.Diff != nil && len(vm.Diff.Files) > 0 {
			keys = append(keys, KeyBinding{Key: "enter", Label: "open in $PAGER"})
		}
		if vm.Diff != nil && len(vm.Diff.Files) > 1 {
			keys = append(keys, KeyBinding{Key: "j/k", Label: "scroll"})
		}
		keys = append(keys, KeyBinding{Key: "tab", Label: "next pane"})
	default:
		keys = LegalKeys(row.State)
		if len(vm.Workers) > 1 {
			keys = append(keys, KeyBinding{Key: "j/k", Label: "switch worker"})
		}
		if vm.Transcript != nil {
			keys = append(keys, KeyBinding{Key: "enter", Label: "inspect"})
		}
		if ok {
			keys = append(keys, KeyBinding{Key: "tab", Label: "next pane"})
		}
	}
	if ok && row.HasDiff {
		label := "diff"
		if vm.DiffOpen {
			label = "hide diff"
		}
		keys = append(keys, KeyBinding{Key: "d", Label: label})
	}
	return keys
}

// relabelTab renames the transcript pane's tab binding for the multi-pane
// frame, where tab cycles every pane rather than returning to the roster.
func relabelTab(keys []KeyBinding) []KeyBinding {
	out := make([]KeyBinding, len(keys))
	for i, k := range keys {
		if k.Key == "tab" {
			k.Label = "next pane"
		}
		out[i] = k
	}
	return out
}

// panel frames body inside a bordered box of the given full width and
// inner height, with title on the top border. The focused panel draws a
// double-line border, so focus reads without colour. Body rows past height
// are dropped and missing rows are padded blank. A zero height fits the body.
func panel(title string, body []string, width, height int, focused bool, style Style) []string {
	if height <= 0 {
		height = len(body)
	}
	inner := width - 2
	if inner < 0 {
		inner = 0
	}
	b := singleBorder
	if focused {
		b = doubleBorder
	}
	border := style.Border
	if focused {
		border = style.FocusBorder
	}
	titleText := " " + title + " "
	if w := lipgloss.Width(titleText); w > inner-2 && inner >= 2 {
		titleText = ansi.Truncate(titleText, inner-2, "…")
	} else if inner < 2 {
		titleText = ""
	}
	fill := inner - 1 - lipgloss.Width(titleText)
	if fill < 0 {
		fill = 0
	}
	top := border.Render(b.tl+b.h) + style.Header.Render(titleText) + border.Render(strings.Repeat(b.h, fill)+b.tr)
	lines := make([]string, 0, height+2)
	lines = append(lines, top)
	for i := range height {
		row := ""
		if i < len(body) {
			row = body[i]
		}
		lines = append(lines, border.Render(b.v)+fitLine(row, inner)+border.Render(b.v))
	}
	lines = append(lines, border.Render(b.bl+strings.Repeat(b.h, inner)+b.br))
	return lines
}

// borderSet holds one box-drawing character set.
type borderSet struct{ tl, tr, bl, br, h, v string }

var (
	singleBorder = borderSet{tl: "┌", tr: "┐", bl: "└", br: "┘", h: "─", v: "│"}
	doubleBorder = borderSet{tl: "╔", tr: "╗", bl: "╚", br: "╝", h: "═", v: "║"}
)

// fitLine truncates line to width cells with an ellipsis and pads it with
// spaces to exactly width, both ANSI-aware. A width of zero or less returns
// the line unchanged.
func fitLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	w := lipgloss.Width(line)
	if w > width {
		line = ansi.Truncate(line, width, "…")
		w = lipgloss.Width(line)
	}
	if w < width {
		line += strings.Repeat(" ", width-w)
	}
	return line
}

// truncLine truncates line to width cells with an ellipsis, ANSI-aware, and
// pads nothing: a notice, the strip, and the footer end where their text
// ends. A width of zero or less returns the line unchanged.
func truncLine(line string, width int) string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return line
	}
	return ansi.Truncate(line, width, "…")
}

// pad fits plain text to exactly width cells: truncated with an ellipsis or
// padded with spaces.
func pad(text string, width int) string { return fitLine(text, width) }

// padLeftTrunc fits a path to width cells, keeping its tail: the file name
// matters more than the leading directories.
func padLeftTrunc(text string, width int) string {
	if w := lipgloss.Width(text); w > width {
		text = ansi.TruncateLeft(text, w-width+1, "…")
	}
	return fitLine(text, width)
}

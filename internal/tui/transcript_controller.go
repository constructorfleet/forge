package tui

// transcript_controller.go holds the transcript-pane plumbing LiveModel and
// PlanningModel share: feed attachment, the read-in-flight guard, the pane
// key handler, height budgeting, and the pager/editor artifact directory.
// None of it depends on WorkerRow or PlanningStageRow, so one definition
// serves both models and a fix to the shared lifecycle cannot drift between
// the two call sites.

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// transcriptController owns the transcript feed and the temp-directory
// artifacts a model's answer/diff/approve controls write, embedded
// anonymously in LiveModel and PlanningModel so their fields (feed, reading,
// ctx, winHeight, artifactDirPath) and these methods promote unchanged.
type transcriptController struct {
	// feed drives the transcript pane. It is the pane's one owner: a nil feed
	// renders the roster (or stage strip) alone.
	feed *TranscriptFeed
	// reading records a feed read in flight, so a tick starts no second one.
	reading bool
	// ctx bounds the feed reads the poll commands run, so quitting the
	// program cancels an in-flight store read instead of waiting it out.
	ctx context.Context

	// winHeight is the last terminal height the runtime reported. Zero means
	// the runtime has sent no size yet: the frame then clips nothing and the
	// tailer keeps its own default.
	winHeight int
	// winWidth is the last terminal width the runtime reported. Zero means the
	// runtime has sent no size yet: the pane then wraps no line.
	winWidth int

	// artifactDirPath holds this session's pager/editor artifacts. A pager or
	// editor killed before its own cleanup callback runs leaves a file, so
	// quit removes the directory.
	artifactDirPath string
}

// SetContext bounds every store read the model performs. Pass the program's
// own context, so a quit cancels an in-flight read.
func (t *transcriptController) SetContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	t.ctx = ctx
}

// handleTranscriptKey applies the pane keys against transcript and focus.
// Tab moves focus; the movement and expand keys act only while the pane
// holds focus, so a roster key and a pane key can share a rune without
// collision.
func (t *transcriptController) handleTranscriptKey(key uv.Key, transcript *TranscriptPane, focus *Pane) {
	if transcript == nil {
		return
	}
	if key.MatchString("tab") {
		if *focus == PaneTranscript {
			*focus = PaneRoster
			return
		}
		*focus = PaneTranscript
		return
	}
	if *focus != PaneTranscript {
		return
	}
	switch {
	case key.MatchString("k", "up"):
		transcript.MoveSelection(-1)
	case key.MatchString("j", "down"):
		transcript.MoveSelection(1)
	case key.MatchString("enter"):
		transcript.ToggleExpand()
	case key.MatchString("pgup"):
		transcript.PageUp()
	case key.MatchString("pgdown"):
		transcript.PageDown()
	case key.MatchString("G"):
		transcript.FollowTail()
	}
}

// applyTranscript commits a finished read into notice and pane, provided it
// still answers wantIssueID, and reports whether it committed. When the read
// is stale — the operator moved the selection to another Worker while this
// read was still in flight — it runs retry instead and reports no commit, so
// a fresh read starts for the Worker now selected rather than let an older
// Worker's transcript land in the pane. A read failure keeps the pane the
// feed already holds and reports the failure in notice, so a transient
// failure never blanks the transcript. LiveModel and PlanningModel share this
// one retry-on-stale path so a future change to it cannot drift between the
// two call sites; the committed bool lets each model stamp its own lag-age
// clock only on a read that actually lands.
func (t *transcriptController) applyTranscript(msg transcriptReadMsg, wantIssueID string, notice *string, pane **TranscriptPane, retry func() tea.Cmd) (cmd tea.Cmd, committed bool) {
	if t.feed == nil || msg.feed != t.feed {
		return nil, false
	}
	t.reading = false
	if msg.read.IssueID() != wantIssueID {
		return retry(), false
	}
	p := t.feed.Apply(msg.read)
	*notice = msg.read.Err()
	if p != nil {
		*pane = p
	}
	return nil, true
}

// sizeFeed sizes the tailer's event window from the transcript row budget and
// the pane's wrap width from the terminal width. The event window and the row
// budget differ in unit: the tailer counts events and the budget counts rows,
// and one event can draw several rows. So the event window is an upper bound
// on how much history to read, and Render (or RenderPlanning) owns the exact
// clip to the terminal; the wrap width is exact, so a long line renders as the
// rows it actually draws and the clip counts them correctly.
func (t *transcriptController) sizeFeed(rows, width int) {
	if t.feed == nil {
		return
	}
	if rows > 0 {
		t.feed.SetHeight(rows)
	}
	t.feed.SetWidth(width)
}

// artifactDir returns this session's pager/editor artifact directory,
// creating it with the given os.MkdirTemp pattern on first use.
func (t *transcriptController) artifactDir(pattern string) (string, error) {
	if t.artifactDirPath != "" {
		return t.artifactDirPath, nil
	}
	dir, err := os.MkdirTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("tui: create artifact directory: %w", err)
	}
	t.artifactDirPath = dir
	return dir, nil
}

// removeArtifacts drops every artifact the session wrote. A failure is
// silent: an observer never aborts on temp-file cleanup.
func (t *transcriptController) removeArtifacts() {
	if t.artifactDirPath == "" {
		return
	}
	_ = os.RemoveAll(t.artifactDirPath)
	t.artifactDirPath = ""
}

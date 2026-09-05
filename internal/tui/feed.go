package tui

// feed.go: the owner that drives the transcript pane. It joins the three read
// seams the pane needs — the Issue's AgentRuns, their transcript tails, and the
// Issue's gate runs — into one poll pass, so the live view holds no knowledge of
// the pane's internals.
//
// A pass has two halves. Fetch does every store read and changes no feed state,
// so it is safe to run on another goroutine. Apply commits the read to the pane.
// The live view runs Fetch in a tea.Cmd, which keeps the store reads off the
// update goroutine that also serves the quit key.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/storage"
)

// TranscriptFeedStore is the read-only slice of storage the feed needs: the
// Issue's AgentRuns, the bounded transcript tail, and the Issue's gate runs.
type TranscriptFeedStore interface {
	TranscriptStore
	AgentRunsByIssue(ctx context.Context, executionID, issueID string) ([]storage.AgentRun, error)
	GateRunsByIssue(ctx context.Context, executionID, issueID string) ([]storage.GateRun, error)
}

// TranscriptFeed owns every Issue's transcript pane and tailer within one
// Execution, keyed by Issue ID. Each Issue is its own context: switching the
// operator's selection to another Issue and back holds the first Issue's
// selection, expansion, and scrollback exactly as the operator left them,
// rather than rebuilding it from scratch. A poll against a different
// Execution starts fresh, because an Issue ID is an Execution-scoped label
// and the same label under another Execution names another Worker.
type TranscriptFeed struct {
	store TranscriptFeedStore

	// executionID names the Execution every held pane belongs to. An Apply
	// against another Execution clears every pane: the Issue ID labels are
	// meaningless across Executions.
	executionID string
	// issues holds one pane and tailer per Issue ID, so a poll for a
	// different Issue never discards another Issue's context.
	issues map[string]*issuePane

	// height is the viewport height a newly built tailer inherits.
	height int
}

// issuePane is one Issue's transcript pane plus the tailer that drives it.
type issuePane struct {
	pane   *TranscriptPane
	tailer *TranscriptTailer
}

// NewTranscriptFeed builds a feed over store. The first Apply builds the pane.
func NewTranscriptFeed(store TranscriptFeedStore) *TranscriptFeed {
	return &TranscriptFeed{store: store}
}

// SetHeight sets the scrollback viewport height in events for every tailer the
// feed owns. A height of zero or less restores the tailer's default. The live
// view calls it from the terminal size, so the pane reads as much history as the
// rows can hold.
//
// The update goroutine alone calls this, while a Fetch for the same feed can run
// in a command goroutine. So Fetch must not read height: the two would then race
// over one field. Only Apply, on the update goroutine, builds a tailer from it.
func (f *TranscriptFeed) SetHeight(h int) {
	f.height = h
	for _, ip := range f.issues {
		if ip.tailer != nil {
			ip.tailer.SetHeight(h)
		}
	}
}

// FeedRead holds one Fetch pass's store reads for one Issue. Apply commits it.
type FeedRead struct {
	executionID string
	issueID     string
	runs        []storage.AgentRun
	tail        transcriptRead
	gates       []GateRow
	// tailed and gated record which reads returned, so Apply can tell an empty
	// answer from a failed one and never overwrites good rows with nothing.
	tailed bool
	gated  bool
	err    error
}

// IssueID returns the Issue the read fetched, so a caller holding several
// concurrent reads can tell which one a FeedRead answers.
func (r FeedRead) IssueID() string {
	return r.issueID
}

// Err returns the pass's read failure as one line. errors.Join separates with a
// newline, and the frame's layout is fixed, so two failures must not take two
// lines.
func (r FeedRead) Err() string {
	if r.err == nil {
		return ""
	}
	return strings.ReplaceAll(r.err.Error(), "\n", "; ")
}

// Fetch performs every store read for one pass and changes no feed state, so it
// runs off the caller's own goroutine. It collects the failures instead of
// stopping at the first, so one dead seam never hides another's data.
func (f *TranscriptFeed) Fetch(ctx context.Context, executionID, issueID string) FeedRead {
	read := FeedRead{executionID: executionID, issueID: issueID}
	var errs []error

	runs, err := f.store.AgentRunsByIssue(ctx, executionID, issueID)
	if err != nil {
		errs = append(errs, fmt.Errorf("tui: agent runs for issue %s: %w", issueID, err))
	}
	read.runs = runs

	if tailer := f.tailerFor(executionID, issueID, runs); tailer != nil {
		read.tailed = true
		read.tail, err = tailer.fetch(ctx, runIDs(runs))
		if err != nil {
			errs = append(errs, err)
		}
	}

	gates, err := f.store.GateRunsByIssue(ctx, executionID, issueID)
	if err != nil {
		errs = append(errs, fmt.Errorf("tui: gate runs for issue %s: %w", issueID, err))
	} else {
		read.gated = true
		read.gates = currentAttemptGateRuns(runs, ConvertGateRuns(gates))
	}

	read.err = errors.Join(errs...)
	return read
}

// Apply commits a fetched pass and returns the Issue's pane to render. A pass
// for another Execution drops every held pane, because an Issue ID is
// meaningless across Executions. A read failure keeps the pane it already
// holds, so one transient failure never blanks the transcript.
func (f *TranscriptFeed) Apply(read FeedRead) *TranscriptPane {
	if f.executionID != read.executionID {
		f.issues = nil
		f.executionID = read.executionID
	}
	ip := f.ensureIssue(read.issueID)
	f.attachRuns(ip, read.runs)
	if ip.tailer != nil && read.tailed {
		ip.pane.SetView(ip.tailer.apply(read.tail))
	}
	if read.gated {
		ip.pane.SetGates(read.gates)
	}
	return ip.pane
}

// Poll performs one whole pass inline. Use Fetch and Apply where the caller must
// keep the store reads off its own goroutine.
func (f *TranscriptFeed) Poll(ctx context.Context, executionID, issueID string) (*TranscriptPane, error) {
	read := f.Fetch(ctx, executionID, issueID)
	return f.Apply(read), read.err
}

// ensureIssue returns the Issue's held pane, building a fresh one on first
// use. The tailer waits for the first AgentRun, because a tailer needs a run
// to attach to.
func (f *TranscriptFeed) ensureIssue(issueID string) *issuePane {
	if f.issues == nil {
		f.issues = make(map[string]*issuePane)
	}
	ip, ok := f.issues[issueID]
	if !ok {
		ip = &issuePane{pane: NewTranscriptPane()}
		f.issues[issueID] = ip
	}
	return ip
}

// tailerFor returns the tailer Fetch reads through, or nil where the Issue holds
// no attempt yet. An Issue's own committed tailer serves the Issue it already
// holds; another Issue, or a first pass, reads through a throwaway tailer,
// because Fetch must not build the state Apply owns. The throwaway tailer keeps
// the default height, because a fetch must not read the height field; the
// window it holds reaches no view, and Apply builds the committed tailer at
// f.height.
func (f *TranscriptFeed) tailerFor(executionID, issueID string, runs []storage.AgentRun) *TranscriptTailer {
	if f.executionID == executionID {
		if ip, ok := f.issues[issueID]; ok && ip.tailer != nil {
			return ip.tailer
		}
	}
	if len(runs) == 0 {
		return nil
	}
	return NewTranscriptTailer(f.store, runs[0].ID, 0)
}

// attachRuns adds every recorded attempt to ip's tailer, oldest first, so the
// retry history is one scrollback with a divider at each boundary.
func (f *TranscriptFeed) attachRuns(ip *issuePane, runs []storage.AgentRun) {
	if len(runs) == 0 {
		return
	}
	if ip.tailer == nil {
		ip.tailer = f.newTailer(runs[0].ID)
		ip.pane.SetScroller(ip.tailer)
	}
	// AddRun ignores a known run, so the first one costs nothing on later polls.
	for _, run := range runs {
		ip.tailer.AddRun(run.ID)
	}
}

// newTailer builds the feed's committed tailer at the current viewport height,
// so a height set before the first attempt is not lost. Apply alone calls it.
func (f *TranscriptFeed) newTailer(agentRunID int64) *TranscriptTailer {
	t := NewTranscriptTailer(f.store, agentRunID, 0)
	t.SetHeight(f.height)
	return t
}

// runIDs lists the AgentRun ids in record order.
func runIDs(runs []storage.AgentRun) []int64 {
	ids := make([]int64, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	return ids
}

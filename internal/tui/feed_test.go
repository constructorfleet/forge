package tui_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
)

// The real Store must satisfy the feed's read seam directly.
var _ tui.TranscriptFeedStore = (storage.Store)(nil)

// fakeFeedStore is a TranscriptFeedStore double: it answers the agent runs, the
// gate runs, and the tail reads for one Issue from fixed logs.
type fakeFeedStore struct {
	// runs holds the AgentRuns per issue, in insertion order.
	runs map[string][]storage.AgentRun
	// events holds each AgentRun's transcript log, keyed by run id.
	events map[int64][]storage.TranscriptEvent
	// gates holds the gate runs per issue.
	gates map[string][]storage.GateRun

	runsErr  error
	gatesErr error
	tailErr  error

	// block holds every read until it closes, which lets a test keep one read
	// pass in flight.
	block chan struct{}
	// byExecution keys the AgentRuns by Execution as well, for the tests that
	// prove the feed scopes its pane to one Execution.
	byExecution map[string]map[string][]storage.AgentRun
}

func (f *fakeFeedStore) AgentRunsByIssue(_ context.Context, executionID, issueID string) ([]storage.AgentRun, error) {
	f.wait()
	if f.runsErr != nil {
		return nil, f.runsErr
	}
	if f.byExecution != nil {
		return f.byExecution[executionID][issueID], nil
	}
	return f.runs[issueID], nil
}

// wait holds the caller while block is open.
func (f *fakeFeedStore) wait() {
	if f.block != nil {
		<-f.block
	}
}

func (f *fakeFeedStore) GateRunsByIssue(_ context.Context, _, issueID string) ([]storage.GateRun, error) {
	f.wait()
	if f.gatesErr != nil {
		return nil, f.gatesErr
	}
	return f.gates[issueID], nil
}

func (f *fakeFeedStore) TranscriptEventsAfter(_ context.Context, runID, afterSeq, limit int64) ([]storage.TranscriptEvent, error) {
	f.wait()
	if f.tailErr != nil {
		return nil, f.tailErr
	}
	var out []storage.TranscriptEvent
	for _, e := range f.events[runID] {
		if int64(e.Seq) > afterSeq {
			out = append(out, e)
		}
		if int64(len(out)) == limit {
			break
		}
	}
	return out, nil
}

// feedFixture builds a store with one attempt, one prose event, one tool call
// with its result, and one failed gate run.
func feedFixture() *fakeFeedStore {
	ran := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	return &fakeFeedStore{
		runs: map[string][]storage.AgentRun{
			"#1": {{ID: 7, ExecutionID: "ex-1", IssueID: "#1"}},
		},
		events: map[int64][]storage.TranscriptEvent{
			7: {
				{AgentRunID: 7, Seq: 0, Type: "MESSAGE", Role: "assistant", Text: "starting work"},
				{AgentRunID: 7, Seq: 1, Type: "TOOL_CALL", ToolName: "bash", ToolInput: "go build ./...", ToolCallID: "t1"},
				{AgentRunID: 7, Seq: 2, Type: "TOOL_RESULT", ToolName: "bash", ToolOutput: "ok", ToolCallID: "t1"},
			},
		},
		gates: map[string][]storage.GateRun{
			"#1": {{Name: "go-test", Command: "go test ./...", ExitCode: 1, Stdout: "FAIL forge", FinishedAt: ran}},
		},
	}
}

// TestFeedPollRendersEventsAndGates proves one feed pass drives the pane with
// the tailer's window and appends the Issue's gate rows after it.
func TestFeedPollRendersEventsAndGates(t *testing.T) {
	feed := tui.NewTranscriptFeed(feedFixture())

	pane, err := feed.Poll(context.Background(), "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if pane == nil {
		t.Fatal("Poll returned a nil pane")
	}
	got := tui.RenderTranscript(pane)
	for _, want := range []string{"starting work", "bash", "gate go-test (fail, exit 1)"} {
		if !strings.Contains(got, want) {
			t.Errorf("render omits %q:\n%s", want, got)
		}
	}
}

// TestFeedPollFoldsRetryAttempts proves a retry's AgentRun extends the same
// scrollback, so the pane draws the attempt dividers.
func TestFeedPollFoldsRetryAttempts(t *testing.T) {
	store := feedFixture()
	store.runs["#1"] = append(store.runs["#1"], storage.AgentRun{ID: 9, ExecutionID: "ex-1", IssueID: "#1"})
	store.events[9] = []storage.TranscriptEvent{
		{AgentRunID: 9, Seq: 0, Type: "MESSAGE", Role: "assistant", Text: "second try"},
	}
	feed := tui.NewTranscriptFeed(store)

	pane, err := feed.Poll(context.Background(), "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	got := tui.RenderTranscript(pane)
	if !strings.Contains(got, "── attempt 2 ──") {
		t.Errorf("render omits the second attempt divider:\n%s", got)
	}
	if !strings.Contains(got, "second try") {
		t.Errorf("render omits the retry's own event:\n%s", got)
	}
}

// TestFeedPollAttachesScroller proves the pane can move its window, so the
// footer's follow-tail key is never inert.
func TestFeedPollAttachesScroller(t *testing.T) {
	feed := tui.NewTranscriptFeed(feedFixture())
	ctx := context.Background()

	pane, err := feed.Poll(ctx, "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	pane.Select(0)
	pane.MoveSelection(-1)
	pane.FollowTail()
	if _, err := feed.Poll(ctx, "ex-1", "#1"); err != nil {
		t.Fatalf("second Poll: %v", err)
	}
	e, ok := pane.SelectedEntry()
	if !ok {
		t.Fatal("pane holds no selection after a follow-tail")
	}
	if e.Event.Seq != 1 {
		t.Errorf("selection seq = %d, want 1: follow tail did not reach the newest event", e.Event.Seq)
	}
}

// TestFeedPollNewIssueStartsFreshPane proves the feed follows the operator's
// Issue: another Issue's transcript never renders in the earlier pane.
func TestFeedPollNewIssueStartsFreshPane(t *testing.T) {
	store := feedFixture()
	store.runs["#2"] = []storage.AgentRun{{ID: 11, ExecutionID: "ex-1", IssueID: "#2"}}
	store.events[11] = []storage.TranscriptEvent{
		{AgentRunID: 11, Seq: 0, Type: "MESSAGE", Role: "assistant", Text: "other issue"},
	}
	feed := tui.NewTranscriptFeed(store)
	ctx := context.Background()

	first, err := feed.Poll(ctx, "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll #1: %v", err)
	}
	second, err := feed.Poll(ctx, "ex-1", "#2")
	if err != nil {
		t.Fatalf("Poll #2: %v", err)
	}
	if first == second {
		t.Fatal("Poll reused the first Issue's pane for the second Issue")
	}
	got := tui.RenderTranscript(second)
	if !strings.Contains(got, "other issue") {
		t.Errorf("render omits the second Issue's event:\n%s", got)
	}
	if strings.Contains(got, "starting work") {
		t.Errorf("render keeps the first Issue's event:\n%s", got)
	}
	if strings.Contains(got, "gate go-test") {
		t.Errorf("render keeps the first Issue's gate row:\n%s", got)
	}
}

// TestFeedPollKeepsEachIssuesOwnPaneAcrossSwitches proves an Issue's pane
// survives the operator switching to another Issue and back: each Issue is its
// own context, not a scratch pane the next selection throws away.
func TestFeedPollKeepsEachIssuesOwnPaneAcrossSwitches(t *testing.T) {
	store := feedFixture()
	store.runs["#2"] = []storage.AgentRun{{ID: 11, ExecutionID: "ex-1", IssueID: "#2"}}
	store.events[11] = []storage.TranscriptEvent{
		{AgentRunID: 11, Seq: 0, Type: "MESSAGE", Role: "assistant", Text: "other issue"},
	}
	feed := tui.NewTranscriptFeed(store)
	ctx := context.Background()

	first, err := feed.Poll(ctx, "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll #1: %v", err)
	}
	first.Select(0)

	if _, err := feed.Poll(ctx, "ex-1", "#2"); err != nil {
		t.Fatalf("Poll #2: %v", err)
	}

	backTo1, err := feed.Poll(ctx, "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll #1 again: %v", err)
	}
	if backTo1 != first {
		t.Fatal("switching back to an Issue rebuilt its pane instead of keeping it")
	}
	e, ok := backTo1.SelectedEntry()
	if !ok {
		t.Fatal("switching back to an Issue lost its pinned selection")
	}
	if e.Event.Seq != 0 {
		t.Errorf("selection seq = %d, want 0: the pinned selection did not survive the switch", e.Event.Seq)
	}
}

// TestFeedPollDropsGatesFromAnEarlierAttempt proves a gate run recorded
// against a finished, earlier AgentRun no longer renders once a later
// AgentRun (a reimplementation) has started: the gate belongs to the attempt
// that failed, not to the one now in progress, and must not linger forever.
func TestFeedPollDropsGatesFromAnEarlierAttempt(t *testing.T) {
	store := feedFixture()
	// The recorded gate run (feedFixture's "go-test") finished at `ran`. A
	// second AgentRun — the reimplementation after the gate failed — starts
	// after that.
	ran := store.gates["#1"][0].FinishedAt
	store.runs["#1"] = append(store.runs["#1"], storage.AgentRun{
		ID: 9, ExecutionID: "ex-1", IssueID: "#1", StartedAt: ran.Add(time.Minute),
	})
	store.events[9] = []storage.TranscriptEvent{
		{AgentRunID: 9, Seq: 0, Type: "MESSAGE", Role: "assistant", Text: "second try"},
	}
	feed := tui.NewTranscriptFeed(store)

	pane, err := feed.Poll(context.Background(), "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	got := tui.RenderTranscript(pane)
	if strings.Contains(got, "gate go-test") {
		t.Errorf("render keeps a gate row from the earlier, reimplemented attempt:\n%s", got)
	}
	if !strings.Contains(got, "second try") {
		t.Errorf("render omits the new attempt's own event:\n%s", got)
	}
}

// TestFeedPollNewExecutionStartsFreshPane proves the feed scopes its pane to one
// Execution. An Issue ID is an Execution-scoped label, so the same label under
// another Execution names another Worker and must not reuse the earlier pane.
func TestFeedPollNewExecutionStartsFreshPane(t *testing.T) {
	store := feedFixture()
	store.byExecution = map[string]map[string][]storage.AgentRun{
		"ex-1": {"#1": {{ID: 7, ExecutionID: "ex-1", IssueID: "#1"}}},
		"ex-2": {"#1": {{ID: 21, ExecutionID: "ex-2", IssueID: "#1"}}},
	}
	store.events[21] = []storage.TranscriptEvent{
		{AgentRunID: 21, Seq: 0, Type: "MESSAGE", Role: "assistant", Text: "other execution"},
	}
	feed := tui.NewTranscriptFeed(store)
	ctx := context.Background()

	first, err := feed.Poll(ctx, "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll ex-1: %v", err)
	}
	second, err := feed.Poll(ctx, "ex-2", "#1")
	if err != nil {
		t.Fatalf("Poll ex-2: %v", err)
	}
	if first == second {
		t.Fatal("Poll reused the first Execution's pane for the second Execution")
	}
	got := tui.RenderTranscript(second)
	if !strings.Contains(got, "other execution") {
		t.Errorf("render omits the second Execution's event:\n%s", got)
	}
	if strings.Contains(got, "starting work") {
		t.Errorf("render keeps the first Execution's event:\n%s", got)
	}
}

// TestFeedReadErrIsOneLine proves two failures in one pass report as one line.
// The frame's layout is fixed, so a second line would shift the strip and the
// footer.
func TestFeedReadErrIsOneLine(t *testing.T) {
	store := feedFixture()
	feed := tui.NewTranscriptFeed(store)
	ctx := context.Background()

	if _, err := feed.Poll(ctx, "ex-1", "#1"); err != nil {
		t.Fatalf("first Poll: %v", err)
	}
	store.tailErr = errors.New("tail is busy")
	store.gatesErr = errors.New("gates are busy")

	read := feed.Fetch(ctx, "ex-1", "#1")
	got := read.Err()
	if strings.Contains(got, "\n") {
		t.Errorf("Err spans several lines:\n%s", got)
	}
	for _, want := range []string{"tail is busy", "gates are busy"} {
		if !strings.Contains(got, want) {
			t.Errorf("Err omits %q, got %q", want, got)
		}
	}
}

// TestFeedApplyGateFailureKeepsGateRows proves a failed gate read keeps the rows
// the pane already holds, rather than clearing them to nothing.
func TestFeedApplyGateFailureKeepsGateRows(t *testing.T) {
	store := feedFixture()
	feed := tui.NewTranscriptFeed(store)
	ctx := context.Background()

	if _, err := feed.Poll(ctx, "ex-1", "#1"); err != nil {
		t.Fatalf("first Poll: %v", err)
	}
	store.gatesErr = errors.New("db is busy")
	pane, err := feed.Poll(ctx, "ex-1", "#1")
	if err == nil {
		t.Fatal("Poll: want the gate read error, got nil")
	}
	if got := tui.RenderTranscript(pane); !strings.Contains(got, "gate go-test") {
		t.Errorf("a failed gate read dropped the retained gate row:\n%s", got)
	}
}

// TestFeedFetchDoesNotChangeThePane proves Fetch is read-only: the pane changes
// only at Apply. The live view runs Fetch on another goroutine, so a Fetch that
// wrote pane state would race the key handler.
func TestFeedFetchDoesNotChangeThePane(t *testing.T) {
	feed := tui.NewTranscriptFeed(feedFixture())
	ctx := context.Background()

	read := feed.Fetch(ctx, "ex-1", "#1")
	first := feed.Apply(read)
	if got := tui.RenderTranscript(first); !strings.Contains(got, "starting work") {
		t.Fatalf("Apply did not commit the first read:\n%s", got)
	}

	// A second Fetch alone must leave the committed window alone, and its read
	// must still carry the events for the Apply that follows.
	next := feed.Fetch(ctx, "ex-1", "#1")
	if got := tui.RenderTranscript(first); !strings.Contains(got, "starting work") {
		t.Errorf("Fetch changed the pane:\n%s", got)
	}
	if second := feed.Apply(next); second != first {
		t.Error("Apply replaced the pane for the same Issue")
	}
}

// TestFeedPollWithoutAgentRunStillShowsGates proves a gate row renders before
// any AgentRun exists, which is the state a planning or gate-only Issue holds.
func TestFeedPollWithoutAgentRunStillShowsGates(t *testing.T) {
	store := feedFixture()
	store.runs["#1"] = nil
	feed := tui.NewTranscriptFeed(store)

	pane, err := feed.Poll(context.Background(), "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if got := tui.RenderTranscript(pane); !strings.Contains(got, "gate go-test") {
		t.Errorf("render omits the gate row with no AgentRun:\n%s", got)
	}
}

// TestFeedPollTailErrorKeepsPane proves a failed read returns the pane it
// already built with its error, so one transient failure never blanks the view.
func TestFeedPollTailErrorKeepsPane(t *testing.T) {
	store := feedFixture()
	feed := tui.NewTranscriptFeed(store)
	ctx := context.Background()

	if _, err := feed.Poll(ctx, "ex-1", "#1"); err != nil {
		t.Fatalf("first Poll: %v", err)
	}
	store.tailErr = errors.New("db is busy")
	pane, err := feed.Poll(ctx, "ex-1", "#1")
	if err == nil {
		t.Fatal("Poll: want the read error, got nil")
	}
	if pane == nil {
		t.Fatal("Poll dropped the pane on a read failure")
	}
	if got := tui.RenderTranscript(pane); !strings.Contains(got, "starting work") {
		t.Errorf("a failed read blanked the retained events:\n%s", got)
	}
}

// TestFeedPollGateErrorKeepsEvents proves a failed gate read still renders the
// event window.
func TestFeedPollGateErrorKeepsEvents(t *testing.T) {
	store := feedFixture()
	store.gatesErr = errors.New("db is busy")
	feed := tui.NewTranscriptFeed(store)

	pane, err := feed.Poll(context.Background(), "ex-1", "#1")
	if err == nil {
		t.Fatal("Poll: want the gate read error, got nil")
	}
	if got := tui.RenderTranscript(pane); !strings.Contains(got, "starting work") {
		t.Errorf("a failed gate read blanked the events:\n%s", got)
	}
}

// TestFeedSetHeightWindowsTheTailer proves the feed passes the viewport height
// to the tailer it owns: a two-event window drops the oldest event.
func TestFeedSetHeightWindowsTheTailer(t *testing.T) {
	feed := tui.NewTranscriptFeed(feedFixture())
	feed.SetHeight(2)

	pane, err := feed.Poll(context.Background(), "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	got := tui.RenderTranscript(pane)
	if strings.Contains(got, "starting work") {
		t.Errorf("a height of 2 kept the oldest event:\n%s", got)
	}
	if !strings.Contains(got, "bash") {
		t.Errorf("render omits the newest events:\n%s", got)
	}
}

// TestFeedSetWidthReachesTheHeldPane proves the feed passes the terminal width
// to a pane it already holds, so a long line wraps to the rows it draws.
func TestFeedSetWidthReachesTheHeldPane(t *testing.T) {
	feed := tui.NewTranscriptFeed(feedFixture())
	pane, err := feed.Poll(context.Background(), "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	unwrapped := strings.Count(tui.RenderTranscript(pane), "\n")

	feed.SetWidth(5)
	if wrapped := strings.Count(tui.RenderTranscript(pane), "\n"); wrapped <= unwrapped {
		t.Errorf("SetWidth(5) after Poll produced %d lines, want more than the unwrapped %d", wrapped, unwrapped)
	}
}

// TestFeedSetWidthBeforePollReachesANewPane proves a width set before a pane
// exists still reaches the pane ensureIssue builds on the first Poll.
func TestFeedSetWidthBeforePollReachesANewPane(t *testing.T) {
	unwidened := tui.NewTranscriptFeed(feedFixture())
	basePane, err := unwidened.Poll(context.Background(), "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	unwrapped := strings.Count(tui.RenderTranscript(basePane), "\n")

	widened := tui.NewTranscriptFeed(feedFixture())
	widened.SetWidth(5)
	pane, err := widened.Poll(context.Background(), "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if wrapped := strings.Count(tui.RenderTranscript(pane), "\n"); wrapped <= unwrapped {
		t.Errorf("a pane built after SetWidth(5) produced %d lines, want more than the unwrapped %d", wrapped, unwrapped)
	}
}

// TestFeedSetHeightAfterPollWindowsTheTailer proves a height set after the pane
// exists reaches the tailer the feed already holds.
func TestFeedSetHeightAfterPollWindowsTheTailer(t *testing.T) {
	feed := tui.NewTranscriptFeed(feedFixture())
	ctx := context.Background()
	if _, err := feed.Poll(ctx, "ex-1", "#1"); err != nil {
		t.Fatalf("first Poll: %v", err)
	}

	feed.SetHeight(2)
	pane, err := feed.Poll(ctx, "ex-1", "#1")
	if err != nil {
		t.Fatalf("second Poll: %v", err)
	}
	if got := tui.RenderTranscript(pane); strings.Contains(got, "starting work") {
		t.Errorf("a height of 2 kept the oldest event:\n%s", got)
	}
}

// manyIssuesFeedStore builds a store with n Issues, each with one AgentRun and
// two prose events, so a test can poll past the feed's pane cap and pin a
// non-default selection to tell a preserved pane from a rebuilt one.
func manyIssuesFeedStore(n int) *fakeFeedStore {
	store := &fakeFeedStore{
		runs:   map[string][]storage.AgentRun{},
		events: map[int64][]storage.TranscriptEvent{},
	}
	for i := 1; i <= n; i++ {
		issueID := fmt.Sprintf("#%d", i)
		runID := int64(100 + i)
		store.runs[issueID] = []storage.AgentRun{{ID: runID, ExecutionID: "ex-1", IssueID: issueID}}
		store.events[runID] = []storage.TranscriptEvent{
			{AgentRunID: runID, Seq: 0, Type: "MESSAGE", Role: "assistant", Text: fmt.Sprintf("issue %d first", i)},
			{AgentRunID: runID, Seq: 1, Type: "MESSAGE", Role: "assistant", Text: fmt.Sprintf("issue %d second", i)},
		}
	}
	return store
}

// TestFeedPollEvictsLeastRecentlyViewedPaneOverCapacity proves the feed's pane
// cache is bounded: polling more Issues than the cap evicts the Issue the
// operator viewed longest ago, so the cache cannot grow without limit while a
// recently viewed Issue still keeps its place.
func TestFeedPollEvictsLeastRecentlyViewedPaneOverCapacity(t *testing.T) {
	const cap = tui.MaxTranscriptPanes
	store := manyIssuesFeedStore(cap + 1)
	feed := tui.NewTranscriptFeed(store)
	ctx := context.Background()

	first, err := feed.Poll(ctx, "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll #1: %v", err)
	}
	// Pin the older event: the pane's default selection is the newest one, so
	// this pin survives only if the pane itself survives.
	first.Select(0)

	// Visit every other Issue once, which keeps #1 the least recently viewed.
	for i := 2; i <= cap+1; i++ {
		issueID := fmt.Sprintf("#%d", i)
		if _, err := feed.Poll(ctx, "ex-1", issueID); err != nil {
			t.Fatalf("Poll %s: %v", issueID, err)
		}
	}

	backTo1, err := feed.Poll(ctx, "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll #1 again: %v", err)
	}
	if backTo1 == first {
		t.Fatal("the cache held #1's pane past its capacity instead of evicting it")
	}
	e, ok := backTo1.SelectedEntry()
	if !ok {
		t.Fatal("the rebuilt pane holds no selection")
	}
	if e.Event.Seq != 1 {
		t.Errorf("selection seq = %d, want 1: the evicted Issue's rebuilt pane still carries the old pinned selection", e.Event.Seq)
	}
}

// TestFeedPollKeepsPanesWithinCapacity proves an Issue viewed within the
// cap's most recent window keeps its pane, so ordinary roster navigation
// among a few Workers never rebuilds a pane the operator just left.
func TestFeedPollKeepsPanesWithinCapacity(t *testing.T) {
	const cap = tui.MaxTranscriptPanes
	store := manyIssuesFeedStore(cap)
	feed := tui.NewTranscriptFeed(store)
	ctx := context.Background()

	first, err := feed.Poll(ctx, "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll #1: %v", err)
	}
	first.Select(0)

	for i := 2; i <= cap; i++ {
		issueID := fmt.Sprintf("#%d", i)
		if _, err := feed.Poll(ctx, "ex-1", issueID); err != nil {
			t.Fatalf("Poll %s: %v", issueID, err)
		}
	}

	backTo1, err := feed.Poll(ctx, "ex-1", "#1")
	if err != nil {
		t.Fatalf("Poll #1 again: %v", err)
	}
	if backTo1 != first {
		t.Fatal("polling within the cap rebuilt #1's pane instead of keeping it")
	}
	e, ok := backTo1.SelectedEntry()
	if !ok {
		t.Fatal("polling within the cap lost #1's selection")
	}
	if e.Event.Seq != 0 {
		t.Errorf("selection seq = %d, want 0: polling within the cap lost #1's pinned selection", e.Event.Seq)
	}
}

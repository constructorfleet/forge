package tui_test

import (
	"context"
	"errors"
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

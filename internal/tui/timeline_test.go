package tui_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
)

// axisCall builds a TOOL_CALL pane event labelled with a review axis.
func axisCall(seq int, id, name, input, axis string) tui.TranscriptEvent {
	e := call(seq, id, name, input)
	e.Subagent = axis
	return e
}

// axisProse builds a MESSAGE pane event labelled with a review axis.
func axisProse(seq int, text, axis string) tui.TranscriptEvent {
	e := prose(seq, text)
	e.Subagent = axis
	return e
}

// TestPaneInterleavesReviewAxesWithInlineLabels proves the three concurrent
// review axes share one pane, in Seq order, each event labelled inline by its
// own axis. No tabs and no sibling panes: the label is the only separation.
func TestPaneInterleavesReviewAxesWithInlineLabels(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{AtTail: true, Events: []tui.TranscriptEvent{
		axisProse(0, "reading the diff", "bugs"),
		axisProse(1, "checking the docs", "docs"),
		axisCall(2, "t1", "bash", "gofmt -l .", "quality"),
		axisProse(3, "found a nil deref", "bugs"),
	}})

	lines := nonEmptyLines(tui.RenderTranscript(pane))
	if len(lines) != 4 {
		t.Fatalf("render has %d lines, want 4:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	wantAxes := []string{"[bugs]", "[docs]", "[quality]", "[bugs]"}
	for i, want := range wantAxes {
		if !strings.Contains(lines[i], want) {
			t.Fatalf("line %d = %q, want the inline axis label %s", i, lines[i], want)
		}
	}
	// Interleaving is by Seq, so the two bugs events keep other axes between them.
	if !strings.Contains(lines[0], "reading the diff") || !strings.Contains(lines[3], "found a nil deref") {
		t.Fatalf("axis events did not interleave in Seq order:\n%s", strings.Join(lines, "\n"))
	}
}

// TestPaneImplementationEventCarriesNoAxisLabel proves the single
// implementation Agent, which has no subagent, renders unlabelled.
func TestPaneImplementationEventCarriesNoAxisLabel(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{AtTail: true, Events: []tui.TranscriptEvent{
		prose(0, "writing the test"),
	}})

	got := tui.RenderTranscript(pane)
	if strings.Contains(got, "[") {
		t.Fatalf("an unlabelled event rendered an axis label: %q", got)
	}
	if !strings.Contains(got, "writing the test") {
		t.Fatalf("render = %q, want the prose text", got)
	}
}

// TestTranscriptDetailStripNamesTheAxis proves the entry detail strip names
// the axis, so a selected event says which review stream produced it.
func TestTranscriptDetailStripNamesTheAxis(t *testing.T) {
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{AtTail: true, Events: []tui.TranscriptEvent{
		axisCall(0, "t1", "bash", "go vet ./...", "quality"),
	}})

	frame := tui.Render(tui.ViewModel{Transcript: pane, Focus: tui.PaneTranscript})
	if !strings.Contains(frame, "axis quality") {
		t.Fatalf("frame = %q, want the detail strip to name the axis", frame)
	}
}

// TestTailerCarriesTheAxisThroughThePoll proves the axis survives the read
// path: the tailer copies transcript_events.subagent into the pane's event, so
// the label is store-sourced and not inferred by the view.
func TestTailerCarriesTheAxisThroughThePoll(t *testing.T) {
	store := &fakeTranscriptStore{events: []storage.TranscriptEvent{
		{Seq: 0, Type: "MESSAGE", Role: "assistant", Text: "reading the diff", Phase: "REVIEWING", Subagent: "bugs"},
		{Seq: 1, Type: "MESSAGE", Role: "assistant", Text: "checking the docs", Phase: "REVIEWING", Subagent: "docs"},
	}}
	tailer := tui.NewTranscriptTailer(store, 7, 0)

	vm, err := tailer.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(vm.Events) != 2 {
		t.Fatalf("len(Events) = %d, want 2", len(vm.Events))
	}
	if vm.Events[0].Subagent != "bugs" || vm.Events[1].Subagent != "docs" {
		t.Fatalf("axes = %q/%q, want bugs/docs", vm.Events[0].Subagent, vm.Events[1].Subagent)
	}
}

// TestRosterFetchPutsTheReviewVerdictOnTheStrip proves the aggregate
// review_runs verdict rides the roster detail strip, not the timeline: the
// per-axis streams carry events, the strip carries the outcome.
func TestRosterFetchPutsTheReviewVerdictOnTheStrip(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "Add axis labels", State: domain.StateReviewing, StateChangedAt: now.Add(-time.Minute)},
			},
		},
		reviews: map[string][]storage.ReviewRun{"#1": {
			{Verdict: "CHANGES_REQUIRED", Diff: "diff --git a/a.go b/a.go\n"},
			{Verdict: "APPROVED", Diff: "diff --git a/b.go b/b.go\n"},
		}},
	}

	vm, err := tui.NewRoster(store, func() time.Time { return now }).Fetch(context.Background(), "ex-1", now)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(vm.Workers) != 1 {
		t.Fatalf("len(Workers) = %d, want 1", len(vm.Workers))
	}
	// The last recorded run is the current verdict.
	if vm.Workers[0].Verdict != "APPROVED" {
		t.Fatalf("Verdict = %q, want APPROVED", vm.Workers[0].Verdict)
	}
	if !vm.Workers[0].HasDiff {
		t.Fatal("HasDiff = false, want true for a review run that stored a diff")
	}

	frame := tui.Render(vm)
	if !strings.Contains(frame, "verdict APPROVED") {
		t.Fatalf("frame = %q, want the verdict on the detail strip", frame)
	}
}

// TestRosterFetchWithNoReviewRunShowsNoVerdict proves an Issue that has not
// been reviewed yet claims no verdict.
func TestRosterFetchWithNoReviewRunShowsNoVerdict(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues:    []domain.Issue{{ID: "#1", Title: "Add axis labels", State: domain.StateImplementing}},
		},
	}

	vm, err := tui.NewRoster(store, func() time.Time { return now }).Fetch(context.Background(), "ex-1", now)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if vm.Workers[0].Verdict != "" {
		t.Fatalf("Verdict = %q, want empty", vm.Workers[0].Verdict)
	}
	if vm.Workers[0].HasDiff {
		t.Fatal("HasDiff = true with no review run")
	}
	if strings.Contains(tui.Render(vm), "verdict APPROVED") {
		t.Fatal("an unreviewed Issue rendered a verdict")
	}
}

// TestRosterFetchReadsNoDiffBlobs proves a poll pass reads the verdict through
// the narrow outcome seam. A poll runs each second per Issue, so it must not
// pull the review runs and their stored diffs only to discard them.
func TestRosterFetchReadsNoDiffBlobs(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "Add axis labels", State: domain.StateReviewing},
			},
		},
		reviews: map[string][]storage.ReviewRun{"#1": {
			{Verdict: "APPROVED", Diff: "diff --git a/a.go b/a.go\n"},
		}},
	}
	roster := tui.NewRoster(store, func() time.Time { return now })

	for range 3 {
		if _, err := roster.Fetch(context.Background(), "ex-1", now); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
	}
	if store.runReads != 0 {
		t.Fatalf("LatestReviewDiff called %d times during polling, want 0", store.runReads)
	}
}

// TestPaneExpandedResultLabelsItsOwnAxis proves the expanded result line reads
// its label off the result event. Pairing is by ToolCallID, not by axis, so the
// result must not inherit the call's label.
func TestPaneExpandedResultLabelsItsOwnAxis(t *testing.T) {
	res := result(1, "t1", "bash", "ok")
	res.Subagent = "docs"
	pane := tui.NewTranscriptPane()
	pane.SetView(tui.TranscriptViewModel{AtTail: true, Events: []tui.TranscriptEvent{
		axisCall(0, "t1", "bash", "gofmt -l .", "bugs"),
		res,
	}})
	pane.ToggleExpand()

	lines := nonEmptyLines(tui.RenderTranscript(pane))
	var resultLine string
	for _, l := range lines {
		if strings.Contains(l, "└") {
			resultLine = l
		}
	}
	if resultLine == "" {
		t.Fatalf("no result line rendered:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(resultLine, "[docs]") {
		t.Fatalf("result line = %q, want its own axis label [docs]", resultLine)
	}
}

package tui_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/needsinfo"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/tui"
)

// fakeAnswerer is a scripted tui.Answerer double: it records every comment
// body it was asked to post, so a test can prove the control fires (or does
// not fire) the tracker POST.
type fakeAnswerer struct {
	mu      sync.Mutex
	calls   []string
	err     error
	block   chan struct{}
	entered chan struct{}
}

func (f *fakeAnswerer) AddComment(_ context.Context, _, body string) (tracker.Comment, error) {
	f.mu.Lock()
	f.calls = append(f.calls, body)
	f.mu.Unlock()
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.block != nil {
		<-f.block
	}
	if f.err != nil {
		return tracker.Comment{}, f.err
	}
	return tracker.Comment{Author: "forge-bot", Body: body, CreatedAt: time.Now()}, nil
}

func (f *fakeAnswerer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// answerFixture builds a live model over one NEEDS_INFO Issue whose
// needs-info checkpoint is recorded.
func answerFixture(t *testing.T, now time.Time) *tui.LiveModel {
	t.Helper()
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "Add axis labels", State: domain.StateNeedsInfo, StateChangedAt: now.Add(-time.Minute)},
			},
		},
		needsInfoCheckpoints: map[string]storage.NeedsInfoCheckpoint{
			"#1": {
				ExecutionID: "ex-1", IssueID: "#1",
				Question: "Which axis should carry the units label?",
			},
		},
	}
	return tui.NewLiveModel(tui.NewRoster(store, func() time.Time { return now }), "ex-1", time.Second)
}

// pressAnswerKey presses the answer key and drives the returned command's
// message back through Update, mirroring pressApproveKey: the store read
// runs inside the command, so the editor only opens on that second pass.
func pressAnswerKey(t *testing.T, m *tui.LiveModel) tea.Cmd {
	t.Helper()
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "a", Code: 'a'}))
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	_, next := m.Update(msg)
	return next
}

// driveAnswer presses the answer key and drives every command the whole flow
// returns -- the checkpoint read, the editor open, and (once the editor
// "closes") the AddComment call -- the same way the Bubble Tea runtime
// would. It returns the rendered frame after the whole chain settles.
func driveAnswer(t *testing.T, m *tui.LiveModel) string {
	t.Helper()
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "a", Code: 'a'}))
	runImmediateCmd(t, m, cmd)
	return m.View().Content
}

// TestLiveModelAnswerKeyDefersToTheEditor proves the answer key hands the
// stored needs-info question to the editor seam, with Forge's own comment
// marker stripped, before ever posting anything.
func TestLiveModelAnswerKeyDefersToTheEditor(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := answerFixture(t, now)
	var opened []string
	m.OpenAnswer = func(_, artifact string) tea.Cmd {
		opened = append(opened, artifact)
		return func() tea.Msg { return nil }
	}
	answerer := &fakeAnswerer{}
	m.Answerer = answerer
	nextPollTick(t, m)

	if cmd := pressAnswerKey(t, m); cmd == nil {
		t.Fatal("the answer key produced no editor command, want the editor's own command")
	}
	if len(opened) != 1 {
		t.Fatalf("editor opened %d times, want 1", len(opened))
	}
	if !strings.Contains(opened[0], "Which axis should carry the units label?") {
		t.Fatalf("editor received %q, want the stored needs-info question", opened[0])
	}
	if answerer.callCount() != 0 {
		t.Fatalf("Answerer called before the editor closed, calls = %v", answerer.calls)
	}
}

// TestLiveModelAnswerStripsTheCommentMarker proves the artifact opened in
// $EDITOR never carries Forge's own hidden comment marker, even when the
// stored question text itself carries one (e.g. read back from a tracker
// comment). This is the "marker stripped when displaying a question"
// acceptance criterion.
func TestLiveModelAnswerStripsTheCommentMarker(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "t", State: domain.StateNeedsInfo, StateChangedAt: now},
			},
		},
		needsInfoCheckpoints: map[string]storage.NeedsInfoCheckpoint{
			"#1": {
				ExecutionID: "ex-1", IssueID: "#1",
				Question: needsinfo.AppendCommentMarker("What is the deploy target?", needsinfo.KindNeedsInfo, "ex-1", "#1"),
			},
		},
	}
	m := tui.NewLiveModel(tui.NewRoster(store, func() time.Time { return now }), "ex-1", time.Second)
	var opened []string
	m.OpenAnswer = func(_, artifact string) tea.Cmd {
		opened = append(opened, artifact)
		return func() tea.Msg { return nil }
	}
	m.Answerer = &fakeAnswerer{}
	nextPollTick(t, m)

	pressAnswerKey(t, m)

	if len(opened) != 1 {
		t.Fatalf("editor opened %d times, want 1", len(opened))
	}
	if strings.Contains(opened[0], "<!-- forge:") {
		t.Fatalf("editor artifact = %q, want Forge's comment marker stripped", opened[0])
	}
	if !strings.Contains(opened[0], "What is the deploy target?") {
		t.Fatalf("editor artifact = %q, want the question text preserved", opened[0])
	}
}

// TestLiveModelAnswerFiresAfterTheEditorCloses proves the editor closing (not
// the key press) is what posts the comment, that the posted comment is a
// plain, marker-free tracker comment, and that answering never resumes: the
// row's own state is left untouched.
func TestLiveModelAnswerFiresAfterTheEditorCloses(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := answerFixture(t, now)
	m.OpenAnswer = func(string, string) tea.Cmd {
		return func() tea.Msg { return tui.AnswerClosedMsg{Text: "The X axis carries the label.\n"} }
	}
	answerer := &fakeAnswerer{}
	m.Answerer = answerer
	nextPollTick(t, m)
	before := m.Workers()[0].State

	got := driveAnswer(t, m)

	if answerer.callCount() != 1 {
		t.Fatalf("Answerer called %d times, want 1", answerer.callCount())
	}
	if !strings.Contains(answerer.calls[0], "The X axis carries the label.") {
		t.Fatalf("Answerer posted %q, want the operator's edited answer", answerer.calls[0])
	}
	if needsinfo.IsForgeComment(answerer.calls[0], needsinfo.KindNeedsInfo, "ex-1", "#1") {
		t.Fatalf("Answerer posted %q, want a plain marker-free comment (answering is not resuming)", answerer.calls[0])
	}
	if m.Workers()[0].State != before {
		t.Fatalf("row state changed to %v before any resume observed it, want %v (answering is not resuming)", m.Workers()[0].State, before)
	}
	if !strings.Contains(got, "#1") {
		t.Fatalf("frame = %q, want an acknowledgement naming the issue", got)
	}
}

// TestLiveModelAnswerSurfacesFailure proves a failed post is surfaced, with
// its typed tracker error, rather than silently dropped.
func TestLiveModelAnswerSurfacesFailure(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := answerFixture(t, now)
	m.OpenAnswer = func(string, string) tea.Cmd {
		return func() tea.Msg { return tui.AnswerClosedMsg{Text: "an answer"} }
	}
	m.Answerer = &fakeAnswerer{err: errors.New("tracker: 401 Unauthorized")}
	nextPollTick(t, m)

	got := driveAnswer(t, m)

	if !strings.Contains(got, "tracker: 401 Unauthorized") {
		t.Fatalf("frame = %q, want the answer failure surfaced", got)
	}
}

// TestLiveModelAnswerInFlightBlocksASecondIssue proves a second answer key
// press while a post is still running does not double-post it.
func TestLiveModelAnswerInFlightBlocksASecondIssue(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := answerFixture(t, now)
	m.OpenAnswer = func(string, string) tea.Cmd {
		return func() tea.Msg { return tui.AnswerClosedMsg{Text: "an answer"} }
	}
	answerer := &fakeAnswerer{block: make(chan struct{}), entered: make(chan struct{})}
	m.Answerer = answerer
	nextPollTick(t, m)

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "a", Code: 'a'}))
	if cmd == nil {
		t.Fatal("the answer key's checkpoint read returned no command")
	}
	msg := cmd()
	_, cmd2 := m.Update(msg)
	if cmd2 == nil {
		t.Fatal("the answer key's editor open returned no command")
	}
	editorMsg := cmd2()
	_, startCmd := m.Update(editorMsg)
	if startCmd == nil {
		t.Fatal("the editor close returned no command")
	}

	result := make(chan tea.Msg, 1)
	go func() { result <- startCmd() }()
	select {
	case <-answerer.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the answer post never started")
	}

	// The first post is now blocked inside the Answerer. A second answer key
	// press must not start a second post.
	got := press(t, m, "a")
	if answerer.callCount() != 1 {
		t.Fatalf("Answerer called %d times while one was in flight, want 1", answerer.callCount())
	}
	if !strings.Contains(got, "in flight") {
		t.Fatalf("frame = %q, want a notice that an answer is already in flight", got)
	}

	close(answerer.block)
	select {
	case msg := <-result:
		m.Update(msg)
	case <-time.After(2 * time.Second):
		t.Fatal("the in-flight answer never returned")
	}
}

// TestLiveModelAnswerWithoutAnAnswererExplains proves a nil Answerer (the
// control not wired up, e.g. verifyTrackerAuth disabled it) explains itself
// instead of silently doing nothing, once the post is actually attempted.
func TestLiveModelAnswerWithoutAnAnswererExplains(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := answerFixture(t, now)
	m.OpenAnswer = func(string, string) tea.Cmd {
		return func() tea.Msg { return tui.AnswerClosedMsg{Text: "an answer"} }
	}
	nextPollTick(t, m)

	got := driveAnswer(t, m)

	if !strings.Contains(got, "not available") {
		t.Fatalf("frame = %q, want a notice that answer is unavailable", got)
	}
}

// TestLiveModelAnswerKeyOnIneligibleRowDoesNothing proves the answer key is
// inert on a row not parked in NEEDS_INFO, mirroring the footer's own
// legality (LegalKeys offers a only for NEEDS_INFO).
func TestLiveModelAnswerKeyOnIneligibleRowDoesNothing(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m, _ := liveFixture(t, now)
	answerer := &fakeAnswerer{}
	m.Answerer = answerer
	nextPollTick(t, m)

	got := press(t, m, "a")

	if answerer.callCount() != 0 {
		t.Fatalf("answer key fired the Answerer on an ineligible row, calls = %v", answerer.calls)
	}
	if strings.Contains(got, "opening needs-info question") {
		t.Fatalf("frame = %q, want no editor armed on an ineligible row", got)
	}
}

// TestLiveModelAnswerWithNoCheckpointExplains proves a NEEDS_INFO row with no
// recorded checkpoint reports that, rather than opening an empty editor.
func TestLiveModelAnswerWithNoCheckpointExplains(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	store := &fakeRosterStore{
		state: storage.ExecutionState{
			Execution: domain.Execution{ID: "ex-1"},
			Issues: []domain.Issue{
				{ID: "#1", Title: "t", State: domain.StateNeedsInfo, StateChangedAt: now},
			},
		},
	}
	m := tui.NewLiveModel(tui.NewRoster(store, func() time.Time { return now }), "ex-1", time.Millisecond)
	called := false
	m.OpenAnswer = func(string, string) tea.Cmd { called = true; return nil }
	nextPollTick(t, m)

	if cmd := pressAnswerKey(t, m); cmd != nil {
		t.Fatal("the answer key produced an editor command with no stored checkpoint")
	}
	if called {
		t.Fatal("the editor opened with no stored checkpoint")
	}
	if !strings.Contains(m.View().Content, "no needs-info checkpoint") {
		t.Fatalf("frame = %q, want a notice that no checkpoint exists", m.View().Content)
	}
}

// TestLiveModelAnswerBlankExplainsAndDoesNotPost proves an operator who
// exits $EDITOR without writing an answer (or clears the whole buffer)
// posts nothing: an empty answer is not a valid tracker comment.
func TestLiveModelAnswerBlankExplainsAndDoesNotPost(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	m := answerFixture(t, now)
	m.OpenAnswer = func(string, string) tea.Cmd {
		return func() tea.Msg { return tui.AnswerClosedMsg{Text: "   \n\n  "} }
	}
	answerer := &fakeAnswerer{}
	m.Answerer = answerer
	nextPollTick(t, m)

	got := driveAnswer(t, m)

	if answerer.callCount() != 0 {
		t.Fatalf("Answerer called %d times with a blank answer, want 0", answerer.callCount())
	}
	if !strings.Contains(got, "empty") {
		t.Fatalf("frame = %q, want a notice that the answer was empty", got)
	}
}

package tui

// answer.go issues the answer control (ADR 0031 / docs/specs/live-agent-tui.md
// "Answering NEEDS_INFO and Decisions"): an in-process tracker POST, not a
// resume, that lets the operator answer a parked NEEDS_INFO Issue inline
// after composing in $EDITOR. The posted comment is a plain, marker-free
// tracker comment: `forge resume` recognizes new human input by marker
// absence plus tracker clock (internal/engine/resume.go), so a marked answer
// would be skipped as Forge's own. internal/needsinfo is imported only to
// strip that marker when displaying a question, never to append one.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Teagan42/forge/internal/needsinfo"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// Answerer is the narrow seam the answer key posts through. A production
// tracker.Tracker satisfies it directly; the TUI depends on no wider Tracker
// surface.
type Answerer interface {
	AddComment(ctx context.Context, issueID, body string) (tracker.Comment, error)
}

// ErrNoNeedsInfoCheckpoint reports that the store holds no needs-info
// checkpoint for the Issue: NEEDS_INFO was reached without one ever being
// recorded.
var ErrNoNeedsInfoCheckpoint = errors.New("tui: no needs-info checkpoint")

// LatestNeedsInfoCheckpoint returns the stored needs-info checkpoint for one
// Issue. It returns ErrNoNeedsInfoCheckpoint when the store holds none.
func LatestNeedsInfoCheckpoint(ctx context.Context, store RosterStore, executionID, issueID string) (storage.NeedsInfoCheckpoint, error) {
	checkpoint, err := store.GetNeedsInfoCheckpoint(ctx, executionID, issueID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return storage.NeedsInfoCheckpoint{}, ErrNoNeedsInfoCheckpoint
		}
		return storage.NeedsInfoCheckpoint{}, fmt.Errorf("tui: read needs-info checkpoint for issue %s: %w", issueID, err)
	}
	return checkpoint, nil
}

// stripCommentMarker removes Forge's own hidden comment marker (see
// needsinfo.CommentMarker) from text, so a question or context read back
// from a tracker comment never leaks the marker into the operator-facing
// $EDITOR artifact. kind namespaces the marker by pause type — KindNeedsInfo
// for an Issue, KindNeedsHuman for a planning Decision — matching whichever
// kind posted the comment being displayed.
func stripCommentMarker(text, kind, executionID, itemID string) string {
	marker := needsinfo.CommentMarker(kind, executionID, itemID)
	return strings.TrimRight(strings.Replace(text, "\n\n"+marker, "", 1), "\n")
}

// renderNeedsInfoQuestion formats a needs-info checkpoint as the $EDITOR
// template the answer key opens: the question (and context, if any) Forge
// asked, as commented-out lines, followed by a blank area for the operator's
// answer.
func renderNeedsInfoQuestion(c storage.NeedsInfoCheckpoint) string {
	var b strings.Builder
	b.WriteString("# Write your answer below. Lines starting with # are ignored.\n#\n")
	b.WriteString("# Question:\n")
	writeCommentedLines(&b, stripCommentMarker(c.Question, needsinfo.KindNeedsInfo, c.ExecutionID, c.IssueID))
	if c.Context != "" {
		b.WriteString("#\n# Context:\n")
		writeCommentedLines(&b, stripCommentMarker(c.Context, needsinfo.KindNeedsInfo, c.ExecutionID, c.IssueID))
	}
	b.WriteString("\n")
	return b.String()
}

// writeCommentedLines writes text to b, one line per "# " prefixed line.
func writeCommentedLines(b *strings.Builder, text string) {
	for _, line := range strings.Split(text, "\n") {
		b.WriteString("# " + line + "\n")
	}
}

// extractAnswer reduces the operator's edited $EDITOR buffer to the posted
// comment body: every "#"-prefixed line (the question/context template) is
// dropped, and the remainder is trimmed. Blank surrounding lines collapse so
// an operator who only edits below the template still gets a clean comment.
func extractAnswer(text string) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// answerNoticeMsg carries an explanation for an answer key that opened no
// editor.
type answerNoticeMsg struct{ text string }

// answerReadyMsg carries a read needs-info checkpoint back to the event
// loop, which owns the editor handover: tea.ExecProcess must come from
// Update and not from inside a command.
type answerReadyMsg struct {
	dir      string
	issueID  string
	artifact string
}

// AnswerClosedMsg reports that the answer artifact's editor exited, carrying
// back the operator's edited text. A failure rides the frame's notice: an
// observer never aborts on a failed artifact edit. Exported (unlike
// answerNoticeMsg) so a test can construct it directly and drive the post
// that follows an editor exit, the same way ApproveClosedMsg is exported for
// the approve control's own tests.
type AnswerClosedMsg struct {
	Text string
	Err  error
}

// OpenAnswerArtifactInEditor writes artifact under dir and opens it in
// $EDITOR, the suspend-and-return mechanic $PAGER's openArtifactInPager
// uses, except the operator's edits are read back once the editor exits.
func OpenAnswerArtifactInEditor(dir, artifact string) tea.Cmd {
	return openArtifactInEditor(dir, artifact, func(text string, err error) tea.Msg {
		return AnswerClosedMsg{Text: text, Err: err}
	})
}

// answerResultMsg carries a finished AddComment call back to the update
// loop. Answering is not resuming: this message never mutates a row's state,
// only the operator's notice.
type answerResultMsg struct {
	issueID string
	err     error
}

// openSelectedAnswer reads the selected Worker's stored needs-info
// checkpoint and defers it to $EDITOR. It returns no command when the row is
// not legal to answer, and reports that on the notice rather than opening an
// editor for nothing.
func (m *LiveModel) openSelectedAnswer() tea.Cmd {
	if m.answerFlow.guard(&m.vm.ActionNotice, "answer") {
		return nil
	}
	row, ok := selectedWorker(m.vm)
	if !ok || !IsAnswerLegal(row.State) {
		m.vm.ActionNotice = "no answerable Worker selected"
		return nil
	}
	// The directory is model state, so the event loop creates it. Only the
	// store read runs inside the command, where a slow store cannot block the
	// frame.
	dir, err := m.artifactDir()
	if err != nil {
		m.vm.ActionNotice = err.Error()
		return nil
	}
	issueID := row.IssueID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), diffReadTimeout)
		defer cancel()
		checkpoint, err := LatestNeedsInfoCheckpoint(ctx, m.Roster.Store, m.ExecutionID, issueID)
		if err != nil {
			return answerNoticeMsg{text: err.Error()}
		}
		return answerReadyMsg{dir: dir, issueID: issueID, artifact: renderNeedsInfoQuestion(checkpoint)}
	}
}

// openAnswer defers a read needs-info question to $EDITOR through the
// injected opener.
func (m *LiveModel) openAnswer(dir, artifact string) tea.Cmd {
	open := m.OpenAnswer
	if open == nil {
		open = OpenAnswerArtifactInEditor
	}
	return open(dir, artifact)
}

// startAnswer returns the command that runs AddComment off the update
// goroutine, so a slow post cannot delay a key press.
func (m *LiveModel) startAnswer(answer string) tea.Cmd {
	if m.Answerer == nil {
		m.answerFlow.close()
		m.vm.ActionNotice = "answer is not available"
		return nil
	}
	issueID := m.answerFlow.issueID
	m.vm.ActionNotice = fmt.Sprintf("posting answer for %s…", issueID)
	answerer, ctx := m.Answerer, m.ctx
	return func() tea.Msg {
		_, err := answerer.AddComment(ctx, issueID, answer)
		return answerResultMsg{issueID: issueID, err: err}
	}
}

// applyAnswerResult commits a finished answer post. Neither a success nor a
// failure is swallowed: the operator sees exactly which Issue was answered,
// or exactly why the post did not go through — the failure is held in the
// notice (in memory only; there is no durable outbox, see
// docs/specs/live-agent-tui.md "Out of scope").
func (m *LiveModel) applyAnswerResult(msg answerResultMsg) {
	m.answerFlow.applyResult(&m.vm.ActionNotice, msg.issueID, msg.err, "answer", "answer posted for %s")
}

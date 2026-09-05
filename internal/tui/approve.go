package tui

// approve.go issues the approve control (ADR 0031 / docs/specs/live-agent-tui.md
// "Approve" row): an in-process, store-only tracker write that moves a
// NEEDS_REPLAN Issue back to READY, after the operator has read the recorded
// replan checkpoint in $PAGER. It pairs the diff control's suspend-and-return
// $PAGER mechanic with a write, the same way the (future) answer control
// pairs $EDITOR with a POST.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

// Approver is the narrow seam the approve key calls into. *engine.Engine
// satisfies it; the TUI depends on no wider Engine surface.
type Approver interface {
	ResumeAfterReplan(ctx context.Context, executionID, issueID string) (domain.Issue, error)
}

// ErrNoReplanCheckpoint reports that the store holds no replan checkpoint for
// the Issue: NEEDS_REPLAN was reached without one ever being recorded.
var ErrNoReplanCheckpoint = errors.New("tui: no replan checkpoint")

// LatestReplanCheckpoint returns the stored replan checkpoint for one Issue.
// It returns ErrNoReplanCheckpoint when the store holds none.
func LatestReplanCheckpoint(ctx context.Context, store RosterStore, executionID, issueID string) (storage.ReplanCheckpoint, error) {
	checkpoint, err := store.GetReplanCheckpoint(ctx, executionID, issueID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return storage.ReplanCheckpoint{}, ErrNoReplanCheckpoint
		}
		return storage.ReplanCheckpoint{}, fmt.Errorf("tui: read replan checkpoint for issue %s: %w", issueID, err)
	}
	return checkpoint, nil
}

// renderReplanCheckpoint formats a replan checkpoint as the plain-text
// artifact the approve key opens in $PAGER.
func renderReplanCheckpoint(c storage.ReplanCheckpoint) string {
	var b strings.Builder
	b.WriteString("Feature: " + c.FeatureID + "\n")
	b.WriteString("Issue: " + c.IssueID + "\n")
	if !c.CreatedAt.IsZero() {
		b.WriteString("Recorded: " + c.CreatedAt.Format(time.RFC3339) + "\n")
	}
	b.WriteString("\nReason:\n" + c.Reason + "\n")
	if c.Evidence != "" {
		b.WriteString("\nEvidence:\n" + c.Evidence + "\n")
	}
	if len(c.AffectedRequirements) > 0 {
		b.WriteString("\nAffected requirements:\n")
		for _, r := range c.AffectedRequirements {
			b.WriteString("- " + r + "\n")
		}
	}
	if c.SuggestedQuestion != "" {
		b.WriteString("\nSuggested question:\n" + c.SuggestedQuestion + "\n")
	}
	if c.PlanRevision != "" {
		b.WriteString("\nPlan revision: " + c.PlanRevision + "\n")
	}
	return b.String()
}

// approveNoticeMsg carries an explanation for an approve key that opened no
// pager.
type approveNoticeMsg struct{ text string }

// approveReadyMsg carries a read replan checkpoint back to the event loop,
// which owns the pager handover: tea.ExecProcess must come from Update and not
// from inside a command.
type approveReadyMsg struct {
	dir      string
	issueID  string
	artifact string
}

// ApproveClosedMsg reports that the approve artifact's pager exited. A
// failure rides the frame's notice: an observer never aborts on a failed
// artifact view. Exported (unlike diffClosedMsg) so a test can construct it
// directly and drive the write that follows a pager exit, the same way
// Workers() is exported for the model's own tests.
type ApproveClosedMsg struct{ Err error }

// OpenApprovalArtifactInPager writes artifact under dir and opens it in
// $PAGER, the same suspend-and-return mechanic OpenDiffInPager uses.
func OpenApprovalArtifactInPager(dir, artifact string) tea.Cmd {
	return openArtifactInPager(dir, artifact, func(err error) tea.Msg { return ApproveClosedMsg{Err: err} })
}

// approveResultMsg carries a finished ResumeAfterReplan call back to the
// update loop. Pending-until-observed means this message never mutates a
// row's state itself: the next roster poll is what shows READY.
type approveResultMsg struct {
	issueID string
	err     error
}

// openSelectedApprove reads the selected Worker's stored replan checkpoint and
// defers it to $PAGER. It returns no command when the row is not legal to
// approve, and reports that on the notice rather than opening a pager for
// nothing.
func (m *LiveModel) openSelectedApprove() tea.Cmd {
	if m.approveFlow.guard(&m.vm.ActionNotice, "approve") {
		return nil
	}
	row, ok := selectedWorker(m.vm)
	if !ok || !IsApproveLegal(row.State) {
		m.vm.ActionNotice = "no approvable Worker selected"
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
		checkpoint, err := LatestReplanCheckpoint(ctx, m.Roster.Store, m.ExecutionID, issueID)
		if err != nil {
			return approveNoticeMsg{text: err.Error()}
		}
		return approveReadyMsg{dir: dir, issueID: issueID, artifact: renderReplanCheckpoint(checkpoint)}
	}
}

// openApprove defers a read replan checkpoint to $PAGER through the injected
// opener.
func (m *LiveModel) openApprove(dir, artifact string) tea.Cmd {
	open := m.OpenApprove
	if open == nil {
		open = OpenApprovalArtifactInPager
	}
	return open(dir, artifact)
}

// startApprove returns the command that runs ResumeAfterReplan off the update
// goroutine, so a slow write cannot delay a key press.
func (m *LiveModel) startApprove() tea.Cmd {
	if m.Approver == nil {
		m.approveFlow.close()
		m.vm.ActionNotice = "approve is not available"
		return nil
	}
	issueID := m.approveFlow.issueID
	m.vm.ActionNotice = fmt.Sprintf("approving issue %s…", issueID)
	approver, ctx, executionID := m.Approver, m.ctx, m.ExecutionID
	return func() tea.Msg {
		_, err := approver.ResumeAfterReplan(ctx, executionID, issueID)
		return approveResultMsg{issueID: issueID, err: err}
	}
}

// applyApproveResult commits a finished approve call. Neither a success nor a
// failure is swallowed: the operator sees exactly which Issue was approved,
// or exactly why the approve did not go through (a FeatureFrozenError included).
func (m *LiveModel) applyApproveResult(msg approveResultMsg) {
	m.approveFlow.applyResult(&m.vm.ActionNotice, msg.issueID, msg.err, "approve", "approve requested for %s")
}

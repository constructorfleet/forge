package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

// OntoRebaser is the workspace-level capability restackDependents uses to
// replay a stacked dependent's own commits onto a merged prerequisite's
// new state (docs/adr/0018; stacked-branch maintenance ticket 3), using the
// pinned old-base SHA (see workerBase) as an explicit rebase boundary
// instead of Rebaser's implicit merge-base search — the boundary a
// squash-merge needs (see workspace.Manager.RebaseOnto's doc comment).
// Optional like Rebaser: a Supervisor.Rebaser that does not also implement
// OntoRebaser (checked via type assertion) leaves restackDependents a
// no-op, so existing callers of New keep compiling and behaving unchanged.
type OntoRebaser interface {
	RebaseOnto(ctx context.Context, executionID, issueID, newBase, oldBase string) (conflictPaths []string, err error)
}

// restackDependents repairs every in-flight, single-parent dependent
// stacked on mergedIssueID once Wait observes that its pull request has
// merged. A dependent that has not started yet (no recorded Workspace) is
// left for the scheduler's base resolver to pick up the merged base when it
// goes READY, and a multi-parent dependent keeps its integration-branch
// behavior — neither is restacked here (docs/adr/0018).
//
// It is a no-op when s.Rebaser does not also implement OntoRebaser, or
// s.Pusher is nil, exactly like pollStale degrades without those optional
// collaborators configured.
//
// A dependent that conflicts, or whose restacked branch does not publish,
// goes to NEEDS_INFO and consumes one unit of its own CI retry budget (see
// routeRestackFailure). The loop then continues with the next dependent,
// because the other dependents of the same merged prerequisite still need a
// restack. Only an infrastructure error — a store call that fails, or a
// rebase that cannot run at all — stops the whole batch.
func (s *Supervisor) restackDependents(ctx context.Context, executionID, mergedIssueID string) error {
	onto, ok := s.Rebaser.(OntoRebaser)
	if !ok || s.Pusher == nil {
		return nil
	}

	issues, err := s.Store.ListIssues(ctx, executionID)
	if err != nil {
		return fmt.Errorf("ci: list issues to restack after issue %s merged: %w", mergedIssueID, err)
	}

	for _, dependent := range issues {
		if !isSingleParentDependentOf(dependent, mergedIssueID) || dependent.State.IsTerminal() {
			continue
		}

		ws, err := s.Store.WorkspaceByIssue(ctx, executionID, dependent.ID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				// Not yet started: the base resolver picks up the merged
				// base once this dependent goes READY.
				continue
			}
			return fmt.Errorf("ci: load workspace for dependent issue %s: %w", dependent.ID, err)
		}

		oldBase, err := s.dependentWorkerBase(ctx, executionID, dependent.ID)
		if err != nil {
			return err
		}

		// The dependent's own published head is read before the rebase moves
		// the local branch, because it is the commit the workspace goes back
		// to if the push of the restacked branch fails.
		publishedHead, err := s.dependentPublishedHead(ctx, executionID, dependent.ID)
		if err != nil {
			return err
		}

		conflicts, err := onto.RebaseOnto(ctx, executionID, dependent.ID, s.BaseBranch, oldBase)
		if err != nil {
			return fmt.Errorf("ci: restack dependent issue %s onto %s: %w", dependent.ID, s.BaseBranch, err)
		}
		if len(conflicts) > 0 {
			// RebaseOnto aborts a conflicted rebase, so the workspace is
			// already back as it was (see workspace.Manager.RebaseOnto).
			details := fmt.Sprintf("restack onto %s after prerequisite issue %s merged conflicted in: %s",
				s.BaseBranch, mergedIssueID, strings.Join(conflicts, ", "))
			if err := s.routeRestackFailure(ctx, executionID, dependent, restackConflictQuestion(s.BaseBranch), details); err != nil {
				return err
			}
			continue
		}

		if err := s.Pusher.ForcePush(ctx, ws.Path, ws.Branch); err != nil {
			details := fmt.Sprintf("force-push of restacked branch %s failed: %s", ws.Branch, err)
			details += s.restoreRestackedBranch(ctx, ws.Path, publishedHead)
			if err := s.routeRestackFailure(ctx, executionID, dependent, restackPushQuestion, details); err != nil {
				return err
			}
			continue
		}
	}

	return nil
}

// restackConflictQuestion is the question a human answers when Forge cannot
// replay a stacked dependent onto its merged prerequisite.
func restackConflictQuestion(baseBranch string) string {
	return "Forge restacked this pull request onto " + baseBranch +
		" after its prerequisite merged, and the rebase hit a conflict that Forge cannot resolve."
}

// restackPushQuestion is the question a human answers when the restack
// itself is clean but Forge cannot publish the result.
const restackPushQuestion = "Forge restacked this pull request after its prerequisite merged, " +
	"but it cannot publish the restacked branch."

// routeRestackFailure ends one dependent's restack attempt at NEEDS_INFO,
// the human-escalation resting state this codebase uses for a conflict, or
// for any other outcome where automated repair would guess at intent (see
// pollConflict and pollStale, which route their own conflicts the same way).
//
// Unlike those two paths, a restack failure also consumes one unit of the
// dependent's CI retry budget. ADR 0018 and issue 288 make restack repair an
// exception to ADR 0017's rule that a conflict detour is free: the budget is
// what stops a stack of dependents from being restacked without limit.
// RecordCIFailure counts nothing more once the ceiling is reached, and
// reports the exhaustion instead, so the human sees why no retry is left.
func (s *Supervisor) routeRestackFailure(ctx context.Context, executionID string, dependent domain.Issue, question, details string) error {
	budgetDetail, err := s.consumeRestackRetryBudget(ctx, executionID, &dependent)
	if err != nil {
		return err
	}
	details += budgetDetail

	run := storage.CIRun{
		ExecutionID: executionID,
		IssueID:     dependent.ID,
		Status:      storage.CIRunStatusFailed,
		// A restack conflict is a merge conflict, so it keeps the conflict
		// kind pollConflict already records for the same failure class.
		Kind:      storage.CIRunKindConflict,
		Details:   capDetails(details, s.Config.CI.MaxOutputBytes),
		CheckedAt: s.Now(),
	}
	if err := s.Store.RecordCIRun(ctx, run); err != nil {
		return fmt.Errorf("ci: persist run for dependent issue %s: %w", dependent.ID, err)
	}

	if _, err := s.routeToNeedsInfo(ctx, executionID, dependent.ID, question, details); err != nil {
		return err
	}
	return nil
}

// consumeRestackRetryBudget records one CI failure against dependent and
// persists the new count. It returns a detail to add to the NEEDS_INFO
// message: an empty string while budget remains, or the exhaustion report
// once the ceiling is reached. An exhausted budget changes no count, so
// there is nothing to persist in that case.
func (s *Supervisor) consumeRestackRetryBudget(ctx context.Context, executionID string, dependent *domain.Issue) (string, error) {
	if err := dependent.RecordCIFailure(); err != nil {
		return "; " + err.Error(), nil
	}
	if err := s.Store.UpdateRetryBudget(ctx, executionID, dependent.ID, dependent.RetryBudget); err != nil {
		return "", fmt.Errorf("ci: persist retry budget for dependent issue %s: %w", dependent.ID, err)
	}
	return "", nil
}

// restoreRestackedBranch puts the live workspace branch back on the
// dependent's last published commit after a failed force-push, and reports
// what it did as a detail for the NEEDS_INFO message. A clean rebase already
// moved the local branch, so without this restoration the workspace holds
// commits that the open pull request does not show.
func (s *Supervisor) restoreRestackedBranch(ctx context.Context, workspacePath, publishedHead string) string {
	switch {
	case s.Resetter == nil:
		return "; no branch resetter is configured, so the workspace keeps the restacked commits"
	case publishedHead == "":
		return "; the last published commit is unknown, so Forge cannot restore the workspace"
	default:
		if err := s.Resetter.Reset(ctx, workspacePath, publishedHead); err != nil {
			return "; restoration of the workspace to " + publishedHead + " failed: " + err.Error()
		}
		return "; Forge restored the workspace to " + publishedHead
	}
}

// dependentPublishedHead reports the commit of the dependent's most recent
// pull request, or an empty string when the dependent published none yet.
// Wait reads a prerequisite's own pull request the same way.
func (s *Supervisor) dependentPublishedHead(ctx context.Context, executionID, issueID string) (string, error) {
	prs, err := s.Store.PullRequestsByIssue(ctx, executionID, issueID)
	if err != nil {
		return "", fmt.Errorf("ci: load pull requests for dependent issue %s: %w", issueID, err)
	}
	if len(prs) == 0 {
		return "", nil
	}
	return prs[len(prs)-1].CommitSHA, nil
}

// isSingleParentDependentOf reports whether dependent has exactly one
// Dependency and it names mergedIssueID as the prerequisite. A dependent
// with more than one Dependency keeps its integration-branch behavior
// instead (docs/adr/0018).
func isSingleParentDependentOf(dependent domain.Issue, mergedIssueID string) bool {
	return len(dependent.Dependencies) == 1 && dependent.Dependencies[0].DependsOnID == mergedIssueID
}

// dependentWorkerBase reads issueID's pinned old-base SHA back from its
// most recent "worker.base_captured" Event — the same datum ADR
// 0018/ticket 330 pins at a stacked dependent's READY transition (or a
// later base refresh, ticket 29). Structurally identical to
// engine.Engine.workerBase; duplicated narrowly here for the same reason as
// NeedsInfoTracker/Rebaser (internal/ci must not import internal/engine —
// see NeedsInfoTracker's doc comment).
func (s *Supervisor) dependentWorkerBase(ctx context.Context, executionID, issueID string) (string, error) {
	events, err := s.Store.EventsByIssue(ctx, executionID, issueID)
	if err != nil {
		return "", fmt.Errorf("ci: load events for dependent issue %s: %w", issueID, err)
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != "worker.base_captured" {
			continue
		}
		var payload struct {
			Base string `json:"base"`
		}
		if err := json.Unmarshal([]byte(events[i].Data), &payload); err != nil {
			return "", fmt.Errorf("ci: decode worker base for dependent issue %s: %w", issueID, err)
		}
		if payload.Base != "" {
			return payload.Base, nil
		}
	}
	return "", fmt.Errorf("ci: dependent issue %s has no captured worker base", issueID)
}

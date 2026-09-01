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

		conflicts, err := onto.RebaseOnto(ctx, executionID, dependent.ID, s.BaseBranch, oldBase)
		if err != nil {
			return fmt.Errorf("ci: restack dependent issue %s onto %s: %w", dependent.ID, s.BaseBranch, err)
		}
		if len(conflicts) > 0 {
			return fmt.Errorf(
				"ci: restack dependent issue %s onto %s conflicted: %s",
				dependent.ID, s.BaseBranch, strings.Join(conflicts, ", "))
		}

		if err := s.Pusher.ForcePush(ctx, ws.Path, ws.Branch); err != nil {
			return fmt.Errorf("ci: force-push restacked branch %s for dependent issue %s: %w", ws.Branch, dependent.ID, err)
		}
	}

	return nil
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

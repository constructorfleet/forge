// Package replan implements the conservative replanning loop's Phase
// 1 <-> Phase 2 bridge (ticket 22): turning an implementation Worker's
// REPLAN_REQUIRED escalation into a created or reopened planning Decision,
// gathering the Feature's completed work as implemented facts a new plan is
// written around, and closing the unstarted Issues a newly approved plan no
// longer contains.
//
// It exists as its own package so internal/engine (Phase 1 orchestration)
// keeps depending only on the narrow seams it declares, and internal/
// decisiongraph and internal/planning (Phase 2) keep performing no I/O.
// Every file and tracker mechanic the loop needs lives here.
package replan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/decisiongraph"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// DecisionStore is the artifact I/O the Decision recorder needs: read the
// Feature's goal (a new Decision records its provenance against it), read
// every existing Decision (to find one to reopen and to keep IDs unique),
// and write one back.
type DecisionStore interface {
	LoadGoal(ctx context.Context, featureID string) (*planning.Artifact, error)
	LoadDecisions(ctx context.Context, featureID string) (map[string]*planning.Artifact, error)
	SaveDecision(ctx context.Context, featureID, decisionID string, decision *planning.Artifact) error
}

// DecisionRecorder implements engine.ReplanDecisionRecorder against a
// DecisionStore.
type DecisionRecorder struct {
	Decisions DecisionStore
}

// RecordReplanTrigger materializes an escalation as a Decision and returns
// its ID. If a Decision already records a trigger from the same reporting
// Issue it is reopened in place (its prior content preserved, the new
// trigger appended, its approval dropped); otherwise a fresh Decision is
// created. Either way the written Decision is unapproved, so planning.Ready
// is false for it and its changed content revision makes every downstream
// artifact evaluate stale through provenance alone.
func (r DecisionRecorder) RecordReplanTrigger(ctx context.Context, featureID, issueID, planRevision string, detail agent.ReplanDetail) (string, error) {
	trigger := decisiongraph.ReplanTrigger{
		IssueID:              issueID,
		Reason:               detail.Reason,
		Evidence:             detail.Evidence,
		AffectedRequirements: detail.AffectedRequirements,
		SuggestedQuestion:    detail.SuggestedQuestion,
		PlanRevision:         planRevision,
	}

	decisions, err := r.Decisions.LoadDecisions(ctx, featureID)
	if err != nil {
		return "", fmt.Errorf("replan: load decisions for feature %s: %w", featureID, err)
	}

	if id, ok := decisiongraph.FindReplanDecision(decisions, issueID); ok {
		reopened := decisiongraph.Reopen(decisions[id], trigger)
		if err := r.Decisions.SaveDecision(ctx, featureID, id, reopened); err != nil {
			return "", fmt.Errorf("replan: save reopened decision %s: %w", id, err)
		}
		return id, nil
	}

	goal, err := r.Decisions.LoadGoal(ctx, featureID)
	if err != nil {
		return "", fmt.Errorf("replan: load goal for feature %s: %w", featureID, err)
	}
	goalRef := decisiongraph.GoalRef{ID: "goal"}
	if goal != nil {
		goalRef.Revision = goal.Revision
	}

	existingIDs := make([]string, 0, len(decisions))
	for id := range decisions {
		existingIDs = append(existingIDs, id)
	}
	sort.Strings(existingIDs)

	materialized, err := decisiongraph.MaterializeReplanTrigger(trigger, goalRef, existingIDs)
	if err != nil {
		return "", fmt.Errorf("replan: materialize replan decision for feature %s: %w", featureID, err)
	}
	if err := r.Decisions.SaveDecision(ctx, featureID, materialized.ID, materialized.Artifact); err != nil {
		return "", fmt.Errorf("replan: save replan decision %s: %w", materialized.ID, err)
	}
	return materialized.ID, nil
}

// IssueReader is the subset of storage.Store the Feature-scoped scans need.
// storage.Store satisfies it; tests can supply a narrower double.
type IssueReader interface {
	ListExecutions(ctx context.Context) ([]storage.ExecutionState, error)
}

// featureIssue pairs an Issue with the Execution it belongs to and the
// provenance parsed from its body.
type featureIssue struct {
	executionID string
	issue       domain.Issue
	provenance  tracker.ForgeProvenance
}

// featureIssues returns every persisted Issue whose Forge Provenance block
// names featureID, across every Execution. Issues carrying no provenance
// block, or a malformed one, are skipped rather than failing the scan: they
// belong to no Feature, so no Feature-scoped decision can apply to them.
func featureIssues(ctx context.Context, store IssueReader, featureID string) ([]featureIssue, error) {
	if featureID == "" {
		return nil, nil
	}
	executions, err := store.ListExecutions(ctx)
	if err != nil {
		return nil, fmt.Errorf("replan: list executions: %w", err)
	}

	var out []featureIssue
	for _, state := range executions {
		for _, issue := range state.Issues {
			prov, err := tracker.ParseForgeProvenance(issue.Body)
			if err != nil || prov == nil || prov.Project != featureID {
				continue
			}
			out = append(out, featureIssue{
				executionID: state.Execution.ID,
				issue:       issue,
				provenance:  *prov,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].issue.ID != out[j].issue.ID {
			return out[i].issue.ID < out[j].issue.ID
		}
		return out[i].executionID < out[j].executionID
	})
	return out, nil
}

// GatherImplementedFacts returns the Feature's completed work as
// PlanningContext facts (acceptance item 3): every Issue in the terminal
// DONE state, carrying the ticket plan revision it was completed under, as
// stamped on its own Forge Provenance block.
//
// Only DONE counts. An Issue that reached a safe suspension boundary during
// a replan is parked in NEEDS_REPLAN — not DONE — precisely so that merely
// finishing mid-replan never promotes it to fact and never lets it count
// toward planning readiness (acceptance item 4). Nothing here mutates,
// reverts, or reopens any completed Issue: it is a read-only scan.
func GatherImplementedFacts(ctx context.Context, store IssueReader, featureID string) ([]planningagent.ImplementedFact, error) {
	issues, err := featureIssues(ctx, store, featureID)
	if err != nil {
		return nil, err
	}

	var facts []planningagent.ImplementedFact
	for _, fi := range issues {
		if fi.issue.State != domain.StateDone {
			continue
		}
		facts = append(facts, planningagent.ImplementedFact{
			IssueID:      fi.issue.ID,
			Summary:      fi.issue.Title,
			PlanRevision: fi.provenance.PlanRevision,
			Requirements: append([]string(nil), fi.provenance.Requirements...),
		})
	}
	return facts, nil
}

// SupersedeStore is the subset of storage.Store SupersedeUnstarted writes
// through.
type SupersedeStore interface {
	IssueReader
	AppendEvent(ctx context.Context, event storage.Event) error
	TransitionIssue(ctx context.Context, executionID, issueID string, to domain.IssueState) (domain.Issue, error)
}

// unstartedStates are the states in which an Issue has not begun real work
// and can therefore be closed as superseded without discarding anything.
// PENDING/BLOCKED_DEPENDENCY/READY have produced no Workspace at all;
// NEEDS_REPLAN is work that was explicitly suspended before it could
// integrate, so the newly approved plan dropping it costs nothing either.
var unstartedStates = map[domain.IssueState]bool{
	domain.StatePending:           true,
	domain.StateBlockedDependency: true,
	domain.StateReady:             true,
	domain.StateNeedsReplan:       true,
}

// SupersedeUnstarted closes every unstarted Issue of featureID that the
// newly approved plan (identified by newPlanRevision) no longer contains,
// and returns their IDs (acceptance item 5).
//
// planned identifies the Issues the new plan does contain. An Issue is kept
// if planned names either its tracker ID or the ticket temp key stamped on
// its Forge Provenance block: a ticket plan is authored in temp keys
// (TKT-003) and only materialization knows the tracker IDs they became, so
// accepting both spellings lets a caller diff against whichever identity it
// actually holds without a materialization round-trip.
//
// Superseded Issues are closed, never recycled: each is transitioned to
// CANCELLED with an "issue.superseded" Event naming the plan revision that
// replaced it. CANCELLED is reused rather than a new terminal state added
// because "superseded" is not a distinct lifecycle — it is an aborted Issue
// with a specific, recorded reason, and every consumer of terminality
// (scheduler readiness, dependency satisfaction, retry) already treats
// CANCELLED correctly.
//
// Issues in any other state are left completely alone: DONE work is fact and
// is never rolled back (item 3), and genuinely in-flight work is quiesced by
// the Feature freeze rather than cancelled here.
func SupersedeUnstarted(ctx context.Context, store SupersedeStore, featureID, newPlanRevision string, planned []string) ([]string, error) {
	keep := make(map[string]bool, len(planned))
	for _, id := range planned {
		keep[id] = true
	}

	issues, err := featureIssues(ctx, store, featureID)
	if err != nil {
		return nil, err
	}

	var superseded []string
	for _, fi := range issues {
		if keep[fi.issue.ID] || keep[fi.provenance.TempKey] || !unstartedStates[fi.issue.State] {
			continue
		}
		data, err := json.Marshal(map[string]string{
			"feature_id":                  featureID,
			"superseded_by_plan_revision": newPlanRevision,
			"previous_plan_revision":      fi.provenance.PlanRevision,
			"previous_state":              string(fi.issue.State),
		})
		if err != nil {
			return superseded, fmt.Errorf("replan: marshal supersede event for issue %s: %w", fi.issue.ID, err)
		}
		if err := store.AppendEvent(ctx, storage.Event{
			ExecutionID: fi.executionID,
			IssueID:     fi.issue.ID,
			Type:        "issue.superseded",
			Data:        string(data),
			OccurredAt:  nowUTC(),
		}); err != nil {
			return superseded, fmt.Errorf("replan: record supersede event for issue %s: %w", fi.issue.ID, err)
		}
		if _, err := store.TransitionIssue(ctx, fi.executionID, fi.issue.ID, domain.StateCancelled); err != nil {
			return superseded, fmt.Errorf("replan: supersede issue %s: %w", fi.issue.ID, err)
		}
		superseded = append(superseded, fi.issue.ID)
	}
	return superseded, nil
}

// ErrNotFrozen is returned by ResumeFeature when the Feature has no active
// replan freeze, so there is nothing to resume.
var ErrNotFrozen = errors.New("replan: feature is not frozen")

// FreezeStore is the subset of storage.Store ResumeFeature writes through.
type FreezeStore interface {
	SupersedeStore
	IsFeatureFrozen(ctx context.Context, featureID string) (bool, storage.FeatureFreeze, error)
	UnfreezeFeature(ctx context.Context, featureID string) error
}

// ResumeFeature performs acceptance item 5's ordering on a newly approved
// plan: first close the unstarted Issues that plan no longer contains, and
// only then lift the Feature freeze. Doing it in this order is what makes
// "a new plan approval is required before frozen work resumes" true — the
// freeze is never lifted while an Issue from the invalidated plan is still
// schedulable.
func ResumeFeature(ctx context.Context, store FreezeStore, featureID, newPlanRevision string, planned []string) ([]string, error) {
	frozen, _, err := store.IsFeatureFrozen(ctx, featureID)
	if err != nil {
		return nil, fmt.Errorf("replan: check freeze for feature %s: %w", featureID, err)
	}
	if !frozen {
		return nil, ErrNotFrozen
	}

	superseded, err := SupersedeUnstarted(ctx, store, featureID, newPlanRevision, planned)
	if err != nil {
		return superseded, err
	}
	if err := store.UnfreezeFeature(ctx, featureID); err != nil {
		return superseded, fmt.Errorf("replan: unfreeze feature %s: %w", featureID, err)
	}
	return superseded, nil
}

// nowUTC stamps Events this package appends directly. storage.SQLiteStore
// stamps its own transition Events with time.Now() internally and has no
// clock seam, so introducing one here would only make the two disagree.
func nowUTC() time.Time { return time.Now().UTC() }

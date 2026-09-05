package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/needsinfo"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// PlanningLeaseAcquirer starts (or reclaims) the Feature's Planning
// Execution, which is what takes the Feature planning lease. *planengine.
// Runtime satisfies it; Engine depends on this one-method interface rather
// than on internal/planengine so the orchestration core stays free of the
// planning runtime, exactly as IssueFetcher/WorkspaceCreator keep it free of
// GitHub and git.
//
// Optional, like NeedsInfoTracker: nil means a REPLAN_REQUIRED escalation
// still freezes the Feature and records its Decision, it just does not open
// a Planning Execution — the freeze, not the lease, is what makes the
// Feature safe.
type PlanningLeaseAcquirer interface {
	Start(ctx context.Context, featureID, baseRevision string) (domain.PlanningExecution, error)
}

// ReplanDecisionRecorder materializes a REPLAN_REQUIRED escalation as a
// planning Decision for the Feature: a fresh Decision when the trigger is
// new, or the existing one reopened (unapproved, with the trigger appended)
// when the same Issue has escalated before. It returns the Decision's ID.
//
// Engine passes the raw payload rather than a Decision-graph type so it does
// not depend on the Phase 2 planning packages; see internal/replan for the
// implementation cmd/forge wires in. Optional like PlanningLeaseAcquirer.
type ReplanDecisionRecorder interface {
	RecordReplanTrigger(ctx context.Context, featureID, issueID, planRevision string, detail agent.ReplanDetail) (decisionID string, err error)
}

// FeatureFrozenError reports that an operation was refused because the
// Issue's Feature has an active replan freeze (CONTEXT.md "Replan"): the
// governing plan has been declared invalid and no new work may start, and
// no in-flight work may integrate, until a fresh plan is approved.
type FeatureFrozenError struct {
	FeatureID string
	IssueID   string
	Reason    string
	Operation string
}

func (e *FeatureFrozenError) Error() string {
	msg := fmt.Sprintf("engine: feature %s is frozen pending replan", e.FeatureID)
	if e.Operation != "" {
		msg += ": " + e.Operation + " refused"
	}
	if e.IssueID != "" {
		msg += fmt.Sprintf(" for issue %s", e.IssueID)
	}
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	return msg
}

// issueProvenance returns the Feature ID and ticket plan revision stamped on
// issue's `## Forge Provenance` block. An Issue with no block at all (hand
// created, predating the planning compiler) yields empty values and no
// error: it belongs to no Feature, so no Feature freeze can apply to it.
func issueProvenance(issue domain.Issue) (featureID, planRevision string, _ error) {
	prov, err := tracker.ParseForgeProvenance(issue.Body)
	if err != nil {
		return "", "", fmt.Errorf("engine: parse forge provenance for issue %s: %w", issue.ID, err)
	}
	if prov == nil {
		return "", "", nil
	}
	return prov.Project, prov.PlanRevision, nil
}

// guardFeatureFrozen refuses to admit issue for execution while its Feature
// is frozen (acceptance item 2's scheduling half): a frozen Feature starts
// no new work. It is deliberately checked before the Issue row is created
// and before any claim is taken, so a frozen Feature leaves no half-started
// Issue behind.
func (e *Engine) guardFeatureFrozen(ctx context.Context, issue domain.Issue) error {
	featureID, _, err := issueProvenance(issue)
	if err != nil {
		return err
	}
	frozen, freeze, err := e.Store.IsFeatureFrozen(ctx, featureID)
	if err != nil {
		return fmt.Errorf("engine: check replan freeze for issue %s: %w", issue.ID, err)
	}
	if !frozen {
		return nil
	}
	return &FeatureFrozenError{
		FeatureID: featureID,
		IssueID:   issue.ID,
		Reason:    freeze.Reason,
		Operation: "starting new work",
	}
}

// guardReplanIntegration is acceptance item 2's integration half and item
// 4's safe suspension boundary. It runs after an in-flight Worker has
// finished committing and pushing its own branch — work that touches nothing
// shared — and immediately before the PR_CREATING transition, which is the
// first step that would integrate this work against the plan a replan has
// invalidated. A frozen Feature parks the Issue in NEEDS_REPLAN instead:
// quiesced, not killed, and not integrated.
//
// suspended reports whether the gate tripped: true means issue is already in
// NEEDS_REPLAN and runCommitAndPR must stop without creating a pull request.
func (e *Engine) guardReplanIntegration(ctx context.Context, executionID, issueID string, issue domain.Issue) (_ domain.Issue, suspended bool, _ error) {
	featureID, _, err := issueProvenance(issue)
	if err != nil {
		return domain.Issue{}, false, err
	}
	frozen, freeze, err := e.Store.IsFeatureFrozen(ctx, featureID)
	if err != nil {
		return domain.Issue{}, false, fmt.Errorf("engine: check replan freeze for issue %s: %w", issueID, err)
	}
	if !frozen {
		return issue, false, nil
	}

	if err := e.appendEvent(ctx, executionID, issueID, "replan.integration_blocked", map[string]string{
		"feature_id":          featureID,
		"reason":              freeze.Reason,
		"triggering_issue":    freeze.TriggeringIssueID,
		"suspension_boundary": "committed and pushed; pull request not created",
	}); err != nil {
		return domain.Issue{}, false, err
	}
	issue, err = e.transition(ctx, executionID, issueID, domain.StateNeedsReplan)
	return issue, true, err
}

// handleReplanRequired implements the StatusReplanRequired arm of
// executeAgent's result switch (acceptance items 1-3): a Worker reporting
// that the plan governing its Issue is invalid escalates structurally rather
// than being repaired.
//
// The order of side effects is load-bearing:
//
//  1. The trigger is checkpointed locally, so the escalation is durable
//     before any external call — the same intent-first discipline
//     handleNeedsInfo documents for its comment.
//  2. The Feature is frozen. This strictly precedes lease acquisition: the
//     freeze is what stops new work and blocks integration, so it must be
//     durable even if acquiring the planning lease then conflicts with a
//     `forge plan` run already in progress. A Feature that got the lease but
//     not the freeze would keep dispatching Workers against a plan already
//     known to be invalid.
//  3. The planning lease is acquired (when a PlanningLeaseAcquirer is
//     wired). A conflict is recorded and tolerated — another Planning
//     Execution already owns replanning this Feature, which is the outcome
//     we wanted anyway — while any other lease failure is fatal.
//  4. The trigger is materialized as a created or reopened Decision, whose
//     changed content moves its revision and therefore makes every
//     downstream artifact evaluate stale through provenance alone.
//
// Nothing here touches completed work: no DONE Issue, Workspace, commit, or
// pull request is reverted, deleted, or reopened (acceptance item 3).
func (e *Engine) handleReplanRequired(ctx context.Context, executionID, issueID, workerRef string, issue domain.Issue, result agent.AgentResult) (domain.Issue, error) {
	if result.Replan == nil {
		return domain.Issue{}, fmt.Errorf("engine: agent reported REPLAN_REQUIRED for issue %s with no Replan detail", issueID)
	}
	if strings.TrimSpace(result.Replan.Reason) == "" {
		return domain.Issue{}, fmt.Errorf("engine: agent reported REPLAN_REQUIRED for issue %s with a blank reason", issueID)
	}

	featureID, planRevision, err := issueProvenance(issue)
	if err != nil {
		return domain.Issue{}, err
	}
	if featureID == "" {
		return domain.Issue{}, fmt.Errorf(
			"engine: issue %s reported REPLAN_REQUIRED but carries no Forge Provenance block, so it belongs to no Feature to freeze", issueID)
	}

	checkpoint, err := e.Store.GetReplanCheckpoint(ctx, executionID, issueID)
	if err != nil && !isNotFound(err) {
		return domain.Issue{}, fmt.Errorf("engine: load replan checkpoint for issue %s: %w", issueID, err)
	}
	if isNotFound(err) {
		checkpoint = storage.ReplanCheckpoint{
			ExecutionID: executionID,
			IssueID:     issueID,
			CreatedAt:   e.Now(),
		}
	}
	checkpoint.FeatureID = featureID
	checkpoint.Reason = result.Replan.Reason
	checkpoint.Evidence = result.Replan.Evidence
	checkpoint.AffectedRequirements = result.Replan.AffectedRequirements
	checkpoint.SuggestedQuestion = result.Replan.SuggestedQuestion
	checkpoint.PlanRevision = planRevision

	label := e.Config.Blocked.Label
	labelEligible := label != "" && e.NeedsInfoTracker != nil
	if labelEligible {
		if err := e.NeedsInfoTracker.AddLabel(ctx, issueID, label); err != nil {
			return domain.Issue{}, fmt.Errorf("engine: add replan label to issue %s: %w", issueID, err)
		}
		checkpoint.LabelAdded = true
	}
	if err := e.Store.SaveReplanCheckpoint(ctx, checkpoint); err != nil {
		return domain.Issue{}, fmt.Errorf("engine: save replan checkpoint for issue %s: %w", issueID, err)
	}

	if err := e.freezeFeature(ctx, executionID, issueID, featureID, &checkpoint); err != nil {
		return domain.Issue{}, err
	}
	if err := e.acquirePlanningLease(ctx, executionID, issueID, featureID, &checkpoint); err != nil {
		return domain.Issue{}, err
	}
	if err := e.recordReplanDecision(ctx, executionID, issueID, featureID, planRevision, *result.Replan, &checkpoint); err != nil {
		return domain.Issue{}, err
	}

	if e.Config.Blocked.Comment && e.NeedsInfoTracker != nil && !checkpoint.CommentPosted {
		body := replanCommentBody(result.Replan, result.Summary, featureID)
		body = needsinfo.AppendCommentMarker(body, needsinfo.KindNeedsInfo, executionID, issueID)
		posted, err := e.NeedsInfoTracker.AddComment(ctx, issueID, body)
		if err != nil {
			return domain.Issue{}, fmt.Errorf("engine: post replan comment on issue %s: %w", issueID, err)
		}
		checkpoint.CommentPosted = true
		checkpoint.CommentAuthor = posted.Author
		checkpoint.CommentPostedAt = posted.CreatedAt
		if err := e.Store.SaveReplanCheckpoint(ctx, checkpoint); err != nil {
			return domain.Issue{}, fmt.Errorf("engine: save replan checkpoint for issue %s: %w", issueID, err)
		}
	}

	if err := e.appendEvent(ctx, executionID, issueID, "replan.checkpoint_saved", map[string]string{
		"feature_id":    featureID,
		"reason":        result.Replan.Reason,
		"plan_revision": planRevision,
		"decision_id":   checkpoint.DecisionID,
	}); err != nil {
		return domain.Issue{}, err
	}

	if err := e.appendEvent(ctx, executionID, issueID, "worker.released", map[string]string{
		"worker_ref": workerRef,
	}); err != nil {
		return domain.Issue{}, err
	}
	if err := e.Store.ReleaseWorkerClaim(ctx, executionID, issueID); err != nil {
		return domain.Issue{}, fmt.Errorf("engine: release worker claim for issue %s: %w", issueID, err)
	}

	return e.transition(ctx, executionID, issueID, domain.StateNeedsReplan)
}

// freezeFeature persists the Feature freeze and records it on the
// checkpoint. See handleReplanRequired for why this strictly precedes
// acquirePlanningLease.
func (e *Engine) freezeFeature(ctx context.Context, executionID, issueID, featureID string, checkpoint *storage.ReplanCheckpoint) error {
	if err := e.Store.FreezeFeature(ctx, featureID, checkpoint.Reason, issueID); err != nil {
		return fmt.Errorf("engine: freeze feature %s for issue %s: %w", featureID, issueID, err)
	}
	checkpoint.Frozen = true
	if err := e.Store.SaveReplanCheckpoint(ctx, *checkpoint); err != nil {
		return fmt.Errorf("engine: save replan checkpoint for issue %s: %w", issueID, err)
	}
	return e.appendEvent(ctx, executionID, issueID, "replan.feature_frozen", map[string]string{
		"feature_id": featureID,
		"reason":     checkpoint.Reason,
	})
}

func (e *Engine) acquirePlanningLease(ctx context.Context, executionID, issueID, featureID string, checkpoint *storage.ReplanCheckpoint) error {
	if e.PlanningLease == nil || checkpoint.LeaseExecutionID != "" {
		return nil
	}

	state, err := e.Store.LoadExecution(ctx, executionID)
	if err != nil {
		return fmt.Errorf("engine: load execution %s for replan lease: %w", executionID, err)
	}

	exec, err := e.PlanningLease.Start(ctx, featureID, state.Execution.BaseRevision)
	if err != nil {
		if errors.Is(err, storage.ErrPlanningLeaseHeld) {
			// Another Planning Execution already owns replanning this
			// Feature. That is the state we were trying to reach, and the
			// freeze above is already durable, so this is recorded rather
			// than failed.
			return e.appendEvent(ctx, executionID, issueID, "replan.lease_conflict", map[string]string{
				"feature_id": featureID,
				"error":      err.Error(),
			})
		}
		return fmt.Errorf("engine: acquire planning lease for feature %s: %w", featureID, err)
	}

	checkpoint.LeaseExecutionID = exec.ID
	if err := e.Store.SaveReplanCheckpoint(ctx, *checkpoint); err != nil {
		return fmt.Errorf("engine: save replan checkpoint for issue %s: %w", issueID, err)
	}
	return e.appendEvent(ctx, executionID, issueID, "replan.lease_acquired", map[string]string{
		"feature_id":            featureID,
		"planning_execution_id": exec.ID,
	})
}

func (e *Engine) recordReplanDecision(ctx context.Context, executionID, issueID, featureID, planRevision string, detail agent.ReplanDetail, checkpoint *storage.ReplanCheckpoint) error {
	if e.ReplanDecisions == nil {
		return nil
	}

	decisionID, err := e.ReplanDecisions.RecordReplanTrigger(ctx, featureID, issueID, planRevision, detail)
	if err != nil {
		return fmt.Errorf("engine: record replan decision for feature %s: %w", featureID, err)
	}

	checkpoint.DecisionID = decisionID
	if err := e.Store.SaveReplanCheckpoint(ctx, *checkpoint); err != nil {
		return fmt.Errorf("engine: save replan checkpoint for issue %s: %w", issueID, err)
	}
	return e.appendEvent(ctx, executionID, issueID, "replan.decision_opened", map[string]string{
		"feature_id":  featureID,
		"decision_id": decisionID,
	})
}

// ResumeAfterReplan is acceptance item 4's post-approval half: the single
// exit from NEEDS_REPLAN back to READY, taken only once the Feature has been
// unfrozen by a fresh plan approval.
//
// Work parked mid-replan is never trusted for having merely finished. Before
// the Issue is released back to READY its Workspace is revalidated against
// the currently configured Quality Gates and the outcome recorded — a result
// that no longer validates re-enters the pipeline for repair rather than
// being carried forward. The Issue returns to READY either way: READY means
// "re-executable", never "done", so a suspended result can never be mistaken
// for completed work (and, being non-DONE, can never enter PlanningContext
// as an implemented fact — see internal/replan.GatherImplementedFacts).
func (e *Engine) ResumeAfterReplan(ctx context.Context, executionID, issueID string) (domain.Issue, error) {
	issue, err := e.Store.GetIssue(ctx, executionID, issueID)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: load issue %s: %w", issueID, err)
	}
	if issue.State != domain.StateNeedsReplan {
		return domain.Issue{}, fmt.Errorf("engine: issue %s is %s, want NEEDS_REPLAN", issueID, issue.State)
	}

	featureID, _, err := issueProvenance(issue)
	if err != nil {
		return domain.Issue{}, err
	}
	frozen, freeze, err := e.Store.IsFeatureFrozen(ctx, featureID)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: check replan freeze for issue %s: %w", issueID, err)
	}
	if frozen {
		return domain.Issue{}, &FeatureFrozenError{
			FeatureID: featureID,
			IssueID:   issueID,
			Reason:    freeze.Reason,
			Operation: "resuming frozen work before a new plan is approved",
		}
	}

	revalidated, detail, err := e.revalidateAfterReplan(ctx, executionID, issueID)
	if err != nil {
		return domain.Issue{}, err
	}
	if err := e.appendEvent(ctx, executionID, issueID, "replan.revalidated", map[string]string{
		"feature_id": featureID,
		"passed":     fmt.Sprint(revalidated),
		"detail":     detail,
	}); err != nil {
		return domain.Issue{}, err
	}

	return e.transition(ctx, executionID, issueID, domain.StateReady)
}

// revalidateAfterReplan re-runs the configured Quality Gates over the
// Issue's preserved Workspace, persisting each Result exactly as the
// VALIDATING stage does (runQualityGates), and reports whether they all
// still pass. An Issue with no recorded Workspace (nothing was ever built
// for it) has nothing to revalidate and reports passed with that noted.
func (e *Engine) revalidateAfterReplan(ctx context.Context, executionID, issueID string) (passed bool, detail string, _ error) {
	ws, err := e.Store.WorkspaceByIssue(ctx, executionID, issueID)
	if err != nil {
		if isNotFound(err) {
			return true, "no workspace to revalidate", nil
		}
		return false, "", fmt.Errorf("engine: load workspace for issue %s: %w", issueID, err)
	}

	// Each gate runs through the environment's single command primitive
	// (ticket 305, constructorfleet/forge#285), exactly as the VALIDATING
	// stage runs them. The loop stops at the first failing gate, which is
	// the Gate Runner's stop-on-first-fail rule (CONTEXT.md "Gate Runner").
	env := e.wrapWorkspace(executionID, issueID, ws)
	passed = true
	detail = "all configured quality gates still pass"
	ran := 0
	for _, g := range e.Config.Quality.Gates {
		res, err := e.runQualityGate(ctx, env, g)
		if err != nil {
			// The command itself could not run, not that it ran and
			// failed — the same deterministic infrastructure failure
			// runQualityGates guards against (constructorfleet/forge#391),
			// so it propagates the same way here rather than reporting a
			// fabricated gate failure.
			return false, "", fmt.Errorf("engine: run revalidation gate %s for issue %s: %w", g.Name, issueID, err)
		}
		ran++
		if err := e.Store.RecordGateRun(ctx, storage.GateRun{
			ExecutionID: executionID,
			IssueID:     issueID,
			Name:        res.Name,
			Command:     res.Command,
			StartedAt:   res.StartedAt,
			FinishedAt:  res.FinishedAt,
			ExitCode:    res.ExitCode,
			Stdout:      res.Stdout,
			Stderr:      res.Stderr,
			Passed:      res.Passed,
		}); err != nil {
			return false, "", fmt.Errorf("engine: record revalidation gate run %s for issue %s: %w", res.Name, issueID, err)
		}
		if !res.Passed {
			passed = false
			detail = "quality gate " + res.Name + " no longer passes; the suspended result must be repaired"
			break
		}
	}
	if ran == 0 {
		detail = "no quality gates configured to revalidate against"
	}
	return passed, detail, nil
}

// replanCommentBody renders the structured comment posted on
// REPLAN_REQUIRED. Each header names exactly the AgentResult field it
// renders, the same discipline needsInfoCommentBody documents.
func replanCommentBody(detail *agent.ReplanDetail, summary, featureID string) string {
	var b strings.Builder
	b.WriteString("Forge has frozen feature ")
	b.WriteString(featureID)
	b.WriteString(" pending replanning: an implementation worker reported that the governing plan is invalid.\n\n")
	b.WriteString("**Reason:** " + detail.Reason)
	if detail.Evidence != "" {
		b.WriteString("\n\n**Evidence:** " + detail.Evidence)
	}
	if len(detail.AffectedRequirements) > 0 {
		b.WriteString("\n\n**Affected requirements:** " + strings.Join(detail.AffectedRequirements, ", "))
	}
	if detail.SuggestedQuestion != "" {
		b.WriteString("\n\n**Suggested question:** " + detail.SuggestedQuestion)
	}
	if summary != "" {
		b.WriteString("\n\n**Summary:** " + summary)
	}
	b.WriteString("\n\nCompleted work is retained. No new work starts and nothing integrates for this feature until a new plan is approved.")
	return b.String()
}

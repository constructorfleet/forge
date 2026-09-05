// Package planningapprove implements tui.PlanningApprover: it finds the one
// Planning Artifact currently NEEDS_APPROVAL for a Feature (the spec or the
// ticket plan — the only two kinds that ever reach that state) and approves
// it at its current content revision. This is the production seam behind
// the planning-phase view's approve key.
package planningapprove

import (
	"context"
	"errors"
	"fmt"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/replan"
	"github.com/Teagan42/forge/internal/ticketplan"
)

// ArtifactStore is the Planning Artifact I/O ApprovePlanningArtifact needs:
// read and write the spec and the ticket plan, the two Artifact kinds a
// Planning Execution ever pauses on for approval.
// *planningfs.FileArtifactLoader satisfies it.
type ArtifactStore interface {
	LoadSpec(ctx context.Context, featureID string) (*planning.Artifact, error)
	SaveSpec(ctx context.Context, featureID string, spec *planning.Artifact) error
	LoadTicketPlan(ctx context.Context, featureID string) (*planning.Artifact, error)
	SaveTicketPlan(ctx context.Context, featureID string, tp *planning.Artifact) error
}

// Locker serializes artifact mutation for one Feature's Planning Artifacts
// against every other holder of the same resource name, in-process and
// across processes. *repolock.Locker satisfies it. A nil Locker (Approver's
// zero value) takes no lock at all: the load-mutate-save sequence runs
// unprotected, as it always has.
type Locker interface {
	WithLock(ctx context.Context, resource string, fn func() error) error
}

// Store is the storage surface ApprovePlanningArtifact reads and writes
// through: the Feature's recorded Planning Executions (to confirm one is
// actually NEEDS_APPROVAL) plus replan.FreezeStore, the same freeze/supersede
// write path resumeFrozenFeature uses in cmd/forge's runApproveTickets.
// *storage.SQLiteStore satisfies it.
type Store interface {
	replan.FreezeStore
	ListPlanningExecutionsByFeature(ctx context.Context, featureID string) ([]domain.PlanningExecution, error)
}

// ErrNoApprovableArtifact reports that the Feature has no Planning Artifact
// currently awaiting approval: either its latest Planning Execution is not
// NEEDS_APPROVAL, or (should the two ever disagree) neither the spec nor the
// ticket plan is actually unapproved.
var ErrNoApprovableArtifact = errors.New("planningapprove: no planning artifact awaiting approval")

// ErrArtifactNotReviewed reports that a Planning Artifact has not passed its
// automated review (SpecificationReview or TicketPlanReview): its State is
// not "reviewed", its review requested changes, or its content was edited
// since the recorded review (ReviewedRevision no longer matches the current
// content revision). `forge approve` requires a recorded APPROVED verdict,
// still current, before a human may approve it.
var ErrArtifactNotReviewed = errors.New("planningapprove: artifact has not passed automated review")

// Approver implements tui.PlanningApprover in production: ApprovePlanningArtifact
// determines which Planning Artifact is the Feature's pending one and
// approves it.
type Approver struct {
	Store     Store
	Artifacts ArtifactStore

	// Locks serializes this Approver's artifact reads and writes against a
	// concurrent `forge plan` (or a second concurrent approval) for the same
	// Feature, so the two never interleave a load and a save of the same
	// file. Optional: nil takes no lock, matching this type's pre-locking
	// behavior.
	Locks Locker
}

// withLock runs fn under the Feature's artifact-mutation lock, or runs it
// directly when no Locker is configured. Every exported entry point below
// acquires this lock at most once per call -- approveSpec and
// approveTicketPlan never lock themselves -- because repolock's underlying
// file lock is not reentrant: a second WithLock call for the same resource,
// nested inside fn, would deadlock.
func (a *Approver) withLock(ctx context.Context, featureID string, fn func() error) error {
	if a.Locks == nil {
		return fn()
	}
	return a.Locks.WithLock(ctx, "planning:"+featureID, fn)
}

// ApprovePlanningArtifact approves whichever Planning Artifact the Feature's
// latest Planning Execution is paused on. The spec is checked before the
// ticket plan: the pipeline (goal -> decisions -> spec -> ticket-plan) puts
// the spec earlier, and a hand-edit can leave the spec unapproved again even
// after a ticket plan already exists, so an unapproved spec always takes
// precedence over the ticket plan. Approving the ticket plan additionally
// resumes the Feature if a replan freeze parked it, mirroring cmd/forge's
// `forge approve tickets`.
func (a *Approver) ApprovePlanningArtifact(ctx context.Context, featureID string) error {
	executions, err := a.Store.ListPlanningExecutionsByFeature(ctx, featureID)
	if err != nil {
		return fmt.Errorf("planningapprove: list planning executions for feature %s: %w", featureID, err)
	}
	if len(executions) == 0 || executions[len(executions)-1].Status != domain.PlanningStatusNeedsApproval {
		return fmt.Errorf("%w: feature %s", ErrNoApprovableArtifact, featureID)
	}

	return a.withLock(ctx, featureID, func() error {
		spec, err := a.Artifacts.LoadSpec(ctx, featureID)
		if err != nil {
			return fmt.Errorf("planningapprove: load spec for feature %s: %w", featureID, err)
		}
		if spec != nil && !planning.Approved(spec) {
			_, err := a.approveSpec(ctx, featureID, spec)
			return err
		}

		ticketPlan, err := a.Artifacts.LoadTicketPlan(ctx, featureID)
		if err != nil {
			return fmt.Errorf("planningapprove: load ticket plan for feature %s: %w", featureID, err)
		}
		if ticketPlan != nil && !planning.Approved(ticketPlan) {
			_, err := a.approveTicketPlan(ctx, featureID, ticketPlan)
			return err
		}

		return fmt.Errorf("%w: feature %s", ErrNoApprovableArtifact, featureID)
	})
}

// ApproveSpec loads the Feature's spec Artifact and approves it at its
// current content revision, returning that revision. It applies no
// precondition on the Feature's Planning Execution status: it is the direct
// seam `forge approve <feature> spec` delegates to.
func (a *Approver) ApproveSpec(ctx context.Context, featureID string) (string, error) {
	var rev string
	err := a.withLock(ctx, featureID, func() error {
		spec, err := a.Artifacts.LoadSpec(ctx, featureID)
		if err != nil {
			return fmt.Errorf("planningapprove: load spec for feature %s: %w", featureID, err)
		}
		if spec == nil {
			return fmt.Errorf("planningapprove: no spec found for feature %s", featureID)
		}
		rev, err = a.approveSpec(ctx, featureID, spec)
		return err
	})
	return rev, err
}

// approveSpec binds spec's ApprovedRevision to its current content revision,
// persists it, and returns that revision.
func (a *Approver) approveSpec(ctx context.Context, featureID string, spec *planning.Artifact) (string, error) {
	if spec.Kind != planning.KindSpec {
		return "", fmt.Errorf("planningapprove: artifact is not a specification for feature %s", featureID)
	}
	if !planning.Reviewed(spec) {
		if !planning.Legacy(spec) {
			return "", fmt.Errorf("%w: specification for feature %s", ErrArtifactNotReviewed, featureID)
		}
		// Written before review-tracking existed: it could only have
		// reached NEEDS_APPROVAL by already passing SpecificationReview
		// under the code that wrote it, so backfill rather than block.
		planning.MarkReviewed(spec)
	}
	rev := planning.ComputeRevision(spec)
	spec.ApprovedRevision = rev
	spec.State = "approved"
	if err := a.Artifacts.SaveSpec(ctx, featureID, spec); err != nil {
		return "", fmt.Errorf("planningapprove: save spec for feature %s: %w", featureID, err)
	}
	return rev, nil
}

// TicketPlanApproval reports the outcome of approving a Feature's ticket
// plan: its new revision, and — if the approval lifted a replan freeze —
// which Issues were closed as superseded by the newly approved plan.
type TicketPlanApproval struct {
	Revision   string
	Resumed    bool
	Superseded []string
}

// ApproveTicketPlan loads the Feature's ticket-plan Artifact and approves it
// at its current content revision, resuming the Feature if a replan freeze
// had parked it. It applies no precondition on the Feature's Planning
// Execution status: it is the direct seam `forge approve <feature> tickets`
// delegates to.
func (a *Approver) ApproveTicketPlan(ctx context.Context, featureID string) (TicketPlanApproval, error) {
	var result TicketPlanApproval
	err := a.withLock(ctx, featureID, func() error {
		tp, err := a.Artifacts.LoadTicketPlan(ctx, featureID)
		if err != nil {
			return fmt.Errorf("planningapprove: load ticket plan for feature %s: %w", featureID, err)
		}
		if tp == nil {
			return fmt.Errorf("planningapprove: no ticket plan found for feature %s", featureID)
		}
		result, err = a.approveTicketPlan(ctx, featureID, tp)
		return err
	})
	return result, err
}

// approveTicketPlan binds tp's ApprovedRevision to its current content
// revision, persists it, and then resumes the Feature if a replan freeze is
// active.
func (a *Approver) approveTicketPlan(ctx context.Context, featureID string, tp *planning.Artifact) (TicketPlanApproval, error) {
	if tp.Kind != planning.KindTicketPlan {
		return TicketPlanApproval{}, fmt.Errorf("planningapprove: artifact is not a ticket plan for feature %s", featureID)
	}
	if !planning.Reviewed(tp) {
		if !planning.Legacy(tp) {
			return TicketPlanApproval{}, fmt.Errorf("%w: ticket plan for feature %s", ErrArtifactNotReviewed, featureID)
		}
		// Written before review-tracking existed: it could only have
		// reached NEEDS_APPROVAL by already passing TicketPlanReview under
		// the code that wrote it, so backfill rather than block.
		planning.MarkReviewed(tp)
	}
	rev := planning.ComputeRevision(tp)
	tp.ApprovedRevision = rev
	tp.State = "approved"
	if err := a.Artifacts.SaveTicketPlan(ctx, featureID, tp); err != nil {
		return TicketPlanApproval{}, fmt.Errorf("planningapprove: save ticket plan for feature %s: %w", featureID, err)
	}

	resumed, superseded, err := a.resumeFrozenFeature(ctx, featureID, rev, tp)
	if err != nil {
		return TicketPlanApproval{}, err
	}
	return TicketPlanApproval{Revision: rev, Resumed: resumed, Superseded: superseded}, nil
}

// resumeFrozenFeature lifts a replan freeze once its new ticket plan is
// approved, reporting which Issues (if any) were closed as superseded. A
// Feature that was never frozen (the ordinary, non-replan approval) is left
// entirely alone.
func (a *Approver) resumeFrozenFeature(ctx context.Context, featureID, planRevision string, plan *planning.Artifact) (bool, []string, error) {
	frozen, _, err := a.Store.IsFeatureFrozen(ctx, featureID)
	if err != nil {
		return false, nil, fmt.Errorf("planningapprove: check replan freeze for feature %s: %w", featureID, err)
	}
	if !frozen {
		return false, nil, nil
	}

	tickets, err := ticketplan.ParseTicketPlan(plan)
	if err != nil {
		return false, nil, fmt.Errorf("planningapprove: parse approved ticket plan for feature %s: %w", featureID, err)
	}
	planned := make([]string, 0, len(tickets))
	for _, t := range tickets {
		planned = append(planned, t.Key)
	}

	superseded, err := replan.ResumeFeature(ctx, a.Store, featureID, planRevision, planned)
	if errors.Is(err, replan.ErrNotFrozen) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return true, superseded, nil
}

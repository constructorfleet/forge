package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FeatureFreeze is a Feature's active replan freeze (ticket 22): the reason
// the governing plan was declared invalid, the Issue whose Worker escalated
// it, and when. While a freeze exists the Feature schedules no new work and
// integrates no in-flight work — see Store.FreezeFeature.
type FeatureFreeze struct {
	FeatureID         string
	Reason            string
	TriggeringIssueID string
	CreatedAt         time.Time
}

// ReplanCheckpoint is the persisted record of one Issue's REPLAN_REQUIRED
// escalation: the structured trigger the Agent reported, the ticket plan
// revision it was working under, and which of the freeze / planning-lease /
// Decision side effects have already run. It is what makes
// engine.handleReplanRequired idempotent across repeats, the same role
// NeedsInfoCheckpoint plays for NEEDS_INFO, and it is the durable source the
// post-approval supersede/revalidate pass reads the trigger back from.
type ReplanCheckpoint struct {
	ExecutionID          string
	IssueID              string
	FeatureID            string
	Reason               string
	Evidence             string
	AffectedRequirements []string
	SuggestedQuestion    string

	// PlanRevision is the ticket plan revision stamped on the reporting
	// Issue's Forge Provenance block — the plan the Agent found invalid.
	PlanRevision string

	// DecisionID is the Decision the trigger was materialized (or reopened)
	// as, empty until that side effect has run.
	DecisionID string

	// Frozen records that the Feature freeze has been persisted. It is set
	// before LeaseExecutionID is ever populated: freeze strictly precedes
	// lease acquisition (see FreezeFeature).
	Frozen bool

	// LeaseExecutionID is the Planning Execution that took the Feature
	// planning lease for this replan, empty until that side effect has run.
	LeaseExecutionID string

	LabelAdded      bool
	CommentPosted   bool
	CommentAuthor   string
	CommentPostedAt time.Time
	CreatedAt       time.Time
}

// FreezeFeature records (or refreshes) featureID's replan freeze.
//
// Freezing is idempotent by primary key: a second escalation for the same
// Feature updates the recorded reason/trigger rather than stacking freezes
// or failing. It never touches feature_planning_leases — the freeze must be
// durable *before* any planning lease is acquired, so that a planning run
// which then conflicts on the lease still leaves the Feature safely frozen
// rather than open for new work.
func (s *SQLiteStore) FreezeFeature(ctx context.Context, featureID, reason, triggeringIssueID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO feature_freezes (feature_id, reason, triggering_issue_id, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (feature_id) DO UPDATE SET
			reason = excluded.reason,
			triggering_issue_id = excluded.triggering_issue_id,
			created_at = excluded.created_at`,
		featureID, reason, triggeringIssueID, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("storage: freeze feature %s: %w", featureID, err)
	}
	return nil
}

// IsFeatureFrozen reports whether featureID currently has a replan freeze,
// returning the freeze itself when it does. An empty featureID (an Issue
// with no Forge Provenance block, e.g. hand-created work predating the
// planning compiler) is never frozen.
func (s *SQLiteStore) IsFeatureFrozen(ctx context.Context, featureID string) (bool, FeatureFreeze, error) {
	if featureID == "" {
		return false, FeatureFreeze{}, nil
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT feature_id, reason, triggering_issue_id, created_at
		FROM feature_freezes WHERE feature_id = ?`,
		featureID,
	)
	var (
		freeze    FeatureFreeze
		createdAt time.Time
	)
	if err := row.Scan(&freeze.FeatureID, &freeze.Reason, &freeze.TriggeringIssueID, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, FeatureFreeze{}, nil
		}
		return false, FeatureFreeze{}, fmt.Errorf("storage: load feature freeze %s: %w", featureID, err)
	}
	freeze.CreatedAt = createdAt.UTC()
	return true, freeze, nil
}

// UnfreezeFeature removes featureID's replan freeze. Unfreezing a Feature
// that is not frozen is a no-op, mirroring ReleaseFeaturePlanningLease.
func (s *SQLiteStore) UnfreezeFeature(ctx context.Context, featureID string) error {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM feature_freezes WHERE feature_id = ?`,
		featureID,
	); err != nil {
		return fmt.Errorf("storage: unfreeze feature %s: %w", featureID, err)
	}
	return nil
}

// SaveReplanCheckpoint persists checkpoint, inserting a new row or replacing
// the existing one for (ExecutionID, IssueID).
func (s *SQLiteStore) SaveReplanCheckpoint(ctx context.Context, checkpoint ReplanCheckpoint) error {
	var commentPostedAt any
	if !checkpoint.CommentPostedAt.IsZero() {
		commentPostedAt = checkpoint.CommentPostedAt.UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO replan_checkpoints (
			execution_id, issue_id, feature_id, reason, evidence,
			affected_requirements, suggested_question, plan_revision, decision_id,
			frozen, lease_execution_id, label_added, comment_posted,
			comment_author, comment_posted_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (execution_id, issue_id) DO UPDATE SET
			feature_id = excluded.feature_id,
			reason = excluded.reason,
			evidence = excluded.evidence,
			affected_requirements = excluded.affected_requirements,
			suggested_question = excluded.suggested_question,
			plan_revision = excluded.plan_revision,
			decision_id = excluded.decision_id,
			frozen = excluded.frozen,
			lease_execution_id = excluded.lease_execution_id,
			label_added = excluded.label_added,
			comment_posted = excluded.comment_posted,
			comment_author = excluded.comment_author,
			comment_posted_at = excluded.comment_posted_at,
			created_at = excluded.created_at`,
		checkpoint.ExecutionID, checkpoint.IssueID, checkpoint.FeatureID,
		checkpoint.Reason, checkpoint.Evidence,
		strings.Join(checkpoint.AffectedRequirements, "\n"),
		checkpoint.SuggestedQuestion, checkpoint.PlanRevision, checkpoint.DecisionID,
		checkpoint.Frozen, checkpoint.LeaseExecutionID,
		checkpoint.LabelAdded, checkpoint.CommentPosted,
		checkpoint.CommentAuthor, commentPostedAt, checkpoint.CreatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("storage: save replan checkpoint %s/%s: %w", checkpoint.ExecutionID, checkpoint.IssueID, err)
	}
	return nil
}

// GetReplanCheckpoint reloads the replan checkpoint for (executionID,
// issueID). Returns ErrNotFound if none has been recorded.
func (s *SQLiteStore) GetReplanCheckpoint(ctx context.Context, executionID, issueID string) (ReplanCheckpoint, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT feature_id, reason, evidence, affected_requirements, suggested_question,
			plan_revision, decision_id, frozen, lease_execution_id,
			label_added, comment_posted, comment_author, comment_posted_at, created_at
		FROM replan_checkpoints WHERE execution_id = ? AND issue_id = ?`,
		executionID, issueID,
	)

	var (
		checkpoint      ReplanCheckpoint
		evidence        sql.NullString
		requirements    sql.NullString
		question        sql.NullString
		planRevision    sql.NullString
		decisionID      sql.NullString
		leaseExecution  sql.NullString
		commentAuthor   sql.NullString
		commentPostedAt sql.NullTime
		createdAt       time.Time
	)
	if err := row.Scan(
		&checkpoint.FeatureID, &checkpoint.Reason, &evidence, &requirements, &question,
		&planRevision, &decisionID, &checkpoint.Frozen, &leaseExecution,
		&checkpoint.LabelAdded, &checkpoint.CommentPosted, &commentAuthor, &commentPostedAt, &createdAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReplanCheckpoint{}, fmt.Errorf("storage: replan checkpoint %s/%s: %w", executionID, issueID, ErrNotFound)
		}
		return ReplanCheckpoint{}, fmt.Errorf("storage: load replan checkpoint %s/%s: %w", executionID, issueID, err)
	}

	checkpoint.ExecutionID = executionID
	checkpoint.IssueID = issueID
	checkpoint.Evidence = evidence.String
	if requirements.String != "" {
		checkpoint.AffectedRequirements = strings.Split(requirements.String, "\n")
	}
	checkpoint.SuggestedQuestion = question.String
	checkpoint.PlanRevision = planRevision.String
	checkpoint.DecisionID = decisionID.String
	checkpoint.LeaseExecutionID = leaseExecution.String
	checkpoint.CommentAuthor = commentAuthor.String
	if commentPostedAt.Valid {
		checkpoint.CommentPostedAt = commentPostedAt.Time.UTC()
	}
	checkpoint.CreatedAt = createdAt.UTC()
	return checkpoint, nil
}

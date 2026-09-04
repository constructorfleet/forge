// Package statusreflect implements ticket 24 ("Execution status reflection
// to the tracker"): a configurable, opt-in signal that an Issue is actively
// being worked, reflected onto the tracker Issue itself (a label swap and,
// optionally, a one-line start comment) so an operator watching the tracker
// can tell a ready-for-agent Issue is already claimed rather than seeing
// nothing change until a pull request appears.
//
// It is a standalone package, not a method on engine.Engine, because two
// independent packages drive Issue state transitions that need to reflect
// onto the tracker: internal/engine (CLAIMED through FAILED) and
// internal/ci (CI_PENDING -> DONE/CI_FAILED, ticket 23's Supervisor, which
// deliberately does not depend on internal/engine).
package statusreflect

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
)

// Tracker is the subset of tracker.Tracker the status-reflection signal
// needs: adding/removing a label and posting a comment. Depending on this
// narrow interface rather than tracker.Tracker keeps callers backend-
// agnostic and their test doubles small, matching engine's
// NeedsInfoTracker/PRCreator convention (see internal/engine/needsinfo.go).
type Tracker interface {
	AddLabel(ctx context.Context, id string, label string) error
	RemoveLabel(ctx context.Context, id string, label string) error
	AddComment(ctx context.Context, id string, body string) (tracker.Comment, error)
}

// startComment is the fixed comment body posted once, when
// config.StatusReflectionConfig.Comment is enabled, on the transition into
// active work (READY -> CLAIMED).
const startComment = "Forge has started work on this issue."

// Label returns the tracker label state should carry under cfg, or "" if
// state carries no status-reflection label at all. Derivation is keyed on
// the canonical IssueState.Group() (internal/domain) instead of a private
// bucket switch, so statusreflect and the TUI agree on every state. The
// status-display groups that have no tracker label map to "".
func Label(cfg config.StatusReflectionConfig, state domain.IssueState) string {
	switch state.Group() {
	case domain.GroupWorking:
		// Forge is actively working the Issue, including the brief window
		// where a commit/push/PR-creation attempt is in flight — the PR
		// is not confirmed to exist until PR_CREATING -> CI_PENDING
		// succeeds, so InReviewLabel would overclaim readiness for review
		// if applied any earlier.
		return cfg.InProgressLabel
	case domain.GroupWaiting:
		// A pull request exists; Forge is waiting on CI (or, in
		// CI_FAILED, about to repair it) rather than actively
		// implementing.
		return cfg.InReviewLabel
	case domain.GroupBlocked:
		// Parked on human input or a provider backoff.
		return cfg.BlockedLabel
	case domain.GroupFailed:
		return cfg.FailedLabel
	default:
		// GroupPending (not yet claimed) and GroupDone (terminal) carry no
		// status-reflection label.
		return ""
	}
}

// Apply reflects an Issue's from -> to transition onto trk, per cfg: it
// swaps whichever label `from` carried (Label) for whichever label `to`
// carries. It does not post the start comment — see StartComment/
// IsStartTransition, called separately by callers that can persist a
// checkpoint (see their doc comments for why the comment needs one and the
// label swap does not).
//
// The relabeling is gated on "did the label actually change" (fromLabel !=
// toLabel): a transition between two states that map to the same label
// (e.g. any hop within the in-progress range, or a repeated call with an
// identical from/to pair after a resume/retry) calls trk not at all.
// Combined with AddLabel/RemoveLabel's idempotent-by-contract semantics
// (see tracker.Tracker), this makes the relabeling naturally idempotent and
// crash-safe across repeats with no persisted checkpoint of its own:
// replaying the same transition is a guaranteed no-op, and a transition
// that did change the label is safe to retry because both tracker calls
// are themselves idempotent.
//
// Apply is a no-op (nil error) when cfg.Enabled is false or trk is nil, so
// every caller is unaffected until an operator opts in (cfg defaults to
// Enabled: false) and a Tracker is wired.
func Apply(ctx context.Context, trk Tracker, cfg config.StatusReflectionConfig, issueID string, from, to domain.IssueState) error {
	if !cfg.Enabled || trk == nil {
		return nil
	}

	fromLabel := Label(cfg, from)
	toLabel := Label(cfg, to)
	if fromLabel == toLabel {
		return nil
	}

	if fromLabel != "" {
		if err := trk.RemoveLabel(ctx, issueID, fromLabel); err != nil {
			return fmt.Errorf("statusreflect: remove label %q from issue %s: %w", fromLabel, issueID, err)
		}
	}
	if toLabel != "" {
		if err := trk.AddLabel(ctx, issueID, toLabel); err != nil {
			return fmt.Errorf("statusreflect: add label %q to issue %s: %w", toLabel, issueID, err)
		}
	}
	return nil
}

// StartComment is the fixed comment body IsStartTransition callers post.
func StartComment() string { return startComment }

// IsStartTransition reports whether from -> to is the transition into
// active work (READY -> CLAIMED) that, when cfg.Comment is set, warrants a
// one-time start comment. Unlike the label swap Apply performs, posting a
// comment is not naturally idempotent — a HTTP POST has no dedup key on the
// tracker side, so a caller acting on a true (not merely a same-label
// replay) result must guard the actual AddComment call with its own
// persisted "already posted" checkpoint, the same way
// internal/engine/needsinfo.go's handleNeedsInfo guards its own comment
// (see storage.NeedsInfoCheckpoint.CommentPosted). This function only
// answers "is this the moment", leaving the checkpoint (and thus the
// AddComment call itself) to the caller.
func IsStartTransition(cfg config.StatusReflectionConfig, from, to domain.IssueState) bool {
	return cfg.Enabled && cfg.Comment && from == domain.StateReady && to == domain.StateClaimed
}

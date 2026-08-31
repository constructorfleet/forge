package ci

import (
	"context"
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/gate"
	"github.com/Teagan42/forge/internal/storage"
)

// WorkspaceConflictResolver implements automatic PR conflict repair using
// Forge's existing Workspace rebase and lease-guarded push seams.
type WorkspaceConflictResolver struct {
	store    storage.Store
	rebaser  Rebaser
	pusher   BranchPusher
	resetter BranchResetter
	gates    gate.CommandRunner
	config   config.Config
}

func NewWorkspaceConflictResolver(store storage.Store, rebaser Rebaser, pusher BranchPusher, resetter BranchResetter, gates gate.CommandRunner, cfg config.Config) *WorkspaceConflictResolver {
	return &WorkspaceConflictResolver{
		store:    store,
		rebaser:  rebaser,
		pusher:   pusher,
		resetter: resetter,
		gates:    gates,
		config:   cfg,
	}
}

func (r *WorkspaceConflictResolver) ResolveMergeConflict(ctx context.Context, req ConflictResolutionRequest) (ConflictResolutionResult, error) {
	ws, err := r.store.WorkspaceByIssue(ctx, req.ExecutionID, req.IssueID)
	if err != nil {
		return ConflictResolutionResult{}, fmt.Errorf("load workspace: %w", err)
	}

	conflicts, err := r.rebaser.Rebase(ctx, req.ExecutionID, req.IssueID, req.BaseBranch)
	if err != nil {
		return ConflictResolutionResult{}, fmt.Errorf("rebase onto %s: %w", req.BaseBranch, err)
	}
	if len(conflicts) > 0 {
		return ConflictResolutionResult{
			Resolved: false,
			Details:  "automatic conflict replay refused: rebase onto " + req.BaseBranch + " conflicted: " + strings.Join(conflicts, ", "),
		}, nil
	}

	if len(r.config.Quality.Gates) > 0 && r.gates == nil {
		return ConflictResolutionResult{
			Resolved: false,
			Details:  "automatic conflict replay refused: quality gates are configured but no gate runner is available",
		}, nil
	}

	results := gate.NewRunner(r.gates).Run(ctx, ws.Path, r.config.Quality.Gates, gate.Options{
		MaxOutputBytes: r.config.Quality.MaxOutputBytes,
	})
	for i := range results {
		res := results[i]
		if err := r.store.RecordGateRun(ctx, storage.GateRun{
			ExecutionID: req.ExecutionID,
			IssueID:     req.IssueID,
			Name:        res.Name,
			Command:     res.Command,
			StartedAt:   res.StartedAt,
			FinishedAt:  res.FinishedAt,
			ExitCode:    res.ExitCode,
			Stdout:      res.Stdout,
			Stderr:      res.Stderr,
			Passed:      res.Passed,
		}); err != nil {
			return ConflictResolutionResult{}, fmt.Errorf("record gate run %s: %w", res.Name, err)
		}
		if !res.Passed {
			if resetErr := r.restoreOriginalHead(ctx, ws.Path, req.PullRequestHeadSHA); resetErr != nil {
				return ConflictResolutionResult{
					Resolved: false,
					Details:  fmt.Sprintf("automatic conflict replay refused: quality gate %s failed with exit code %d; restore to %s failed: %v", res.Name, res.ExitCode, req.PullRequestHeadSHA, resetErr),
				}, nil
			}
			return ConflictResolutionResult{
				Resolved: false,
				Details:  fmt.Sprintf("automatic conflict replay refused: quality gate %s failed with exit code %d", res.Name, res.ExitCode),
			}, nil
		}
	}

	if err := r.pusher.ForcePush(ctx, ws.Path, ws.Branch); err != nil {
		if resetErr := r.restoreOriginalHead(ctx, ws.Path, req.PullRequestHeadSHA); resetErr != nil {
			return ConflictResolutionResult{
				Resolved: false,
				Details:  "automatic conflict replay refused: force-push failed: " + err.Error() + "; restore to " + req.PullRequestHeadSHA + " failed: " + resetErr.Error(),
			}, nil
		}
		return ConflictResolutionResult{
			Resolved: false,
			Details:  "automatic conflict replay refused: force-push failed: " + err.Error(),
		}, nil
	}

	details := "rebased branch onto " + req.BaseBranch + ", quality gates passed, and force-pushed branch"
	if len(r.config.Quality.Gates) == 0 {
		details = "rebased branch onto " + req.BaseBranch + " and force-pushed branch; no quality gates configured"
	}
	return ConflictResolutionResult{Resolved: true, Details: details}, nil
}

func (r *WorkspaceConflictResolver) restoreOriginalHead(ctx context.Context, workspacePath, commitSHA string) error {
	if commitSHA == "" {
		return fmt.Errorf("recorded pull request head SHA is empty")
	}
	if r.resetter == nil {
		return fmt.Errorf("no branch resetter configured")
	}
	return r.resetter.Reset(ctx, workspacePath, commitSHA)
}

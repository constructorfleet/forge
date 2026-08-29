package config

import (
	"errors"
	"fmt"
	"strings"
)

// FieldError identifies one invalid field in a Config, naming the offending
// field path and value so the caller can locate the problem in .forge.yaml
// without guessing.
type FieldError struct {
	Field   string
	Value   string
	Message string
}

func (e *FieldError) Error() string {
	if e.Value == "" {
		return fmt.Sprintf("%s: %s", e.Field, e.Message)
	}
	return fmt.Sprintf("%s: %s (got %q)", e.Field, e.Message, e.Value)
}

func fieldErr(field, value, message string) *FieldError {
	return &FieldError{Field: field, Value: value, Message: message}
}

// validate checks a resolved Config for problems that resolve() cannot
// catch by construction — unknown enum values, out-of-range numbers, and
// structurally required fields left empty. It collects every problem found
// rather than stopping at the first, so a single Load call reports all
// offending fields at once.
func validate(cfg Config) error {
	var errs []error

	if cfg.Version < 1 {
		errs = append(errs, fieldErr("version", fmt.Sprint(cfg.Version), "must be >= 1"))
	}

	if cfg.Tracker.Type != "github" {
		errs = append(errs, fieldErr("tracker.type", cfg.Tracker.Type, "unsupported tracker type; supported: github"))
	}

	if strings.TrimSpace(cfg.Git.Base) == "" {
		errs = append(errs, fieldErr("git.base", cfg.Git.Base, "must not be empty"))
	}
	if strings.TrimSpace(cfg.Git.BranchTemplate) == "" {
		errs = append(errs, fieldErr("git.branch_template", cfg.Git.BranchTemplate, "must not be empty"))
	} else {
		if !strings.Contains(cfg.Git.BranchTemplate, "{issue}") {
			errs = append(errs, fieldErr("git.branch_template", cfg.Git.BranchTemplate, "must contain the {issue} placeholder"))
		}
		if !strings.Contains(cfg.Git.BranchTemplate, "{execution}") {
			// Without {execution}, two Executions touching the same Issue
			// would collide on one branch name, breaking Workspace
			// isolation (see internal/workspace).
			errs = append(errs, fieldErr("git.branch_template", cfg.Git.BranchTemplate, "must contain the {execution} placeholder"))
		}
	}
	if strings.TrimSpace(cfg.Git.WorktreeRoot) == "" {
		errs = append(errs, fieldErr("git.worktree_root", cfg.Git.WorktreeRoot, "must not be empty"))
	}

	if cfg.Execution.MaxParallel < 1 {
		errs = append(errs, fieldErr("execution.max_parallel", fmt.Sprint(cfg.Execution.MaxParallel), "must be >= 1"))
	}

	if cfg.Retry.Gate < 0 {
		errs = append(errs, fieldErr("retry.gate", fmt.Sprint(cfg.Retry.Gate), "must be >= 0"))
	}
	if cfg.Retry.Review < 0 {
		errs = append(errs, fieldErr("retry.review", fmt.Sprint(cfg.Retry.Review), "must be >= 0"))
	}
	if cfg.Retry.CI < 0 {
		errs = append(errs, fieldErr("retry.ci", fmt.Sprint(cfg.Retry.CI), "must be >= 0"))
	}

	if cfg.Workflow.Implementation != "tdd" {
		errs = append(errs, fieldErr("workflow.implementation", cfg.Workflow.Implementation, "unsupported implementation mode; supported: tdd"))
	}

	for i, g := range cfg.Quality.Gates {
		if strings.TrimSpace(g.Name) == "" {
			errs = append(errs, fieldErr(fmt.Sprintf("quality.gates[%d].name", i), g.Name, "must not be empty"))
		}
		if strings.TrimSpace(g.Command) == "" {
			errs = append(errs, fieldErr(fmt.Sprintf("quality.gates[%d].command", i), g.Command, "must not be empty"))
		}
	}

	if strings.TrimSpace(cfg.PullRequests.CommitMessageTemplate) == "" {
		errs = append(errs, fieldErr("pull_requests.commit_message_template", cfg.PullRequests.CommitMessageTemplate, "must not be empty"))
	}

	switch cfg.CI.MergeRequirements.Mode {
	case MergeRequirementsGitHub:
		// authoritative, no further constraints
	case MergeRequirementsExplicit:
		if len(cfg.CI.MergeRequirements.Checks) == 0 {
			errs = append(errs, fieldErr("ci.required_checks.checks", "", "must list at least one check when mode is explicit"))
		}
	default:
		errs = append(errs, fieldErr("ci.required_checks.mode", string(cfg.CI.MergeRequirements.Mode), "unsupported mode; supported: github, explicit"))
	}
	if cfg.CI.PollInterval <= 0 {
		errs = append(errs, fieldErr("ci.poll_interval", fmt.Sprint(cfg.CI.PollInterval), "must be > 0"))
	}
	if cfg.CI.MaxOutputBytes < 1 {
		errs = append(errs, fieldErr("ci.max_output_bytes", fmt.Sprint(cfg.CI.MaxOutputBytes), "must be >= 1"))
	}

	if strings.TrimSpace(cfg.Blocked.Label) == "" {
		errs = append(errs, fieldErr("blocked.label", cfg.Blocked.Label, "must not be empty"))
	}

	if strings.TrimSpace(cfg.Agent.Provider) == "" {
		errs = append(errs, fieldErr("agent.provider", cfg.Agent.Provider, "must not be empty"))
	}

	if cfg.Quality.MaxOutputBytes < 1 {
		errs = append(errs, fieldErr("quality.max_output_bytes", fmt.Sprint(cfg.Quality.MaxOutputBytes), "must be >= 1"))
	}

	for issueID, deps := range cfg.Dependencies.Overrides {
		if strings.TrimSpace(issueID) == "" {
			errs = append(errs, fieldErr("dependencies.overrides", "", "issue ID key must not be empty"))
			continue
		}
		for _, dep := range deps {
			if strings.TrimSpace(dep) == "" {
				errs = append(errs, fieldErr(fmt.Sprintf("dependencies.overrides[%s]", issueID), "", "dependency ID must not be empty"))
			}
			if dep == issueID {
				errs = append(errs, fieldErr(fmt.Sprintf("dependencies.overrides[%s]", issueID), dep, "issue cannot depend on itself"))
			}
		}
	}

	if cfg.StatusReflection.Enabled && strings.TrimSpace(cfg.StatusReflection.InProgressLabel) == "" {
		errs = append(errs, fieldErr("status_reflection.in_progress_label", cfg.StatusReflection.InProgressLabel, "must not be empty when status_reflection.enabled is true"))
	}

	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

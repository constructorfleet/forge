package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Teagan42/forge/internal/planning"
)

const goalUsage = `Usage: forge goal init <feature-id> [--force]

Create .forge/features/<feature-id>/goal.md, the human-authored Planning
Artifact that seeds 'forge plan'. The generated file is a skeleton with
placeholder prose under four sections (Goal, Context, Constraints, Success
Criteria) for the author to fill in, already stamped with a valid content
revision so it is not Stale.

  --force   Overwrite an existing goal.md and re-stamp a fresh draft.
`

// runGoalInit implements `forge goal init <feature-id> [--force]`.
func runGoalInit(args []string) int {
	featureID, force, code, done := parseGoalInitArgs(args)
	if done {
		return code
	}

	if err := validateFeatureID(featureID); err != nil {
		fmt.Fprintf(os.Stderr, "forge goal init: %v\n", err)
		return 1
	}

	path := filepath.Join(".forge", "features", featureID, "goal.md")
	if !force {
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintf(os.Stderr, "forge goal init: %s already exists; rerun with --force to overwrite\n", path)
			return 1
		} else if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "forge goal init: stat %s: %v\n", path, err)
			return 1
		}
	}

	goal := buildGoalSkeleton()

	ctx := context.Background()
	loader := &fileArtifactLoader{featureID: featureID}
	if err := loader.SaveGoal(ctx, featureID, goal); err != nil {
		fmt.Fprintf(os.Stderr, "forge goal init: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "wrote %s\n", path)
	return 0
}

// buildGoalSkeleton builds a fresh draft goal.md Artifact: four `##`
// sections with placeholder prose, and a Revision stamped from that content
// so the file is never Stale as written. derived_from and approval fields
// are left empty -- a goal is a pipeline root, never itself approved.
func buildGoalSkeleton() *planning.Artifact {
	a := &planning.Artifact{
		Kind:  planning.KindGoal,
		State: "draft",
		Sections: []planning.Section{
			{Heading: "Goal", Body: "Describe the outcome this feature should achieve, in one or two sentences."},
			{Heading: "Context", Body: "Explain why this feature is needed now: the problem, the trigger, and any relevant background."},
			{Heading: "Constraints", Body: "List any hard limits the solution must respect (technical, product, timeline, or otherwise)."},
			{Heading: "Success Criteria", Body: "Describe how to tell the feature succeeded, ideally as observable, testable outcomes."},
		},
	}
	a.Revision = planning.ComputeRevision(a)
	return a
}

// parseGoalInitArgs parses `forge goal init <feature-id> [--force]`'s
// arguments. done is true when runGoalInit should return immediately with
// code (help text, or a parse error).
func parseGoalInitArgs(args []string) (featureID string, force bool, code int, done bool) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(os.Stdout, goalUsage)
		return "", false, 0, true
	}

	if args[0] != "init" {
		fmt.Fprintf(os.Stderr, "forge goal: unknown subcommand %q\n\n%s", args[0], goalUsage)
		return "", false, 1, true
	}

	rest := args[1:]
	for _, a := range rest {
		switch a {
		case "--help", "-h":
			fmt.Fprint(os.Stdout, goalUsage)
			return "", false, 0, true
		case "--force":
			force = true
		default:
			if featureID != "" {
				fmt.Fprintf(os.Stderr, "too many arguments: %v\n\n%s", rest, goalUsage)
				return "", false, 1, true
			}
			featureID = a
		}
	}
	if featureID == "" {
		fmt.Fprintf(os.Stderr, "feature-id is required\n\n%s", goalUsage)
		return "", false, 1, true
	}
	return featureID, force, 0, false
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Teagan42/forge/internal/planning"
)

const goalUsage = `Usage: forge goal init <feature-id> [--force] [--from <path>] [--from-issue [<n>]] [--edit]

Create .forge/features/<feature-id>/goal.md, the human-authored Planning
Artifact that seeds 'forge plan'. The generated file is a skeleton with
placeholder prose under four sections (Goal, Context, Constraints, Success
Criteria) for the author to fill in, already stamped with a valid content
revision so it is not Stale.

  --force        Overwrite an existing goal.md and re-stamp a fresh draft.
  --from <path>  Adopt an existing freeform doc as the goal instead of
                 scaffolding a blank skeleton: its content is split into
                 '##' sections (content before the first heading becomes a
                 leading section; a doc with no headings becomes a single
                 'Goal' section) and stamped as a fresh draft.
  --from-issue [<n>]
                 Fetch GitHub issue <n> with 'gh issue view' and seed the
                 goal from its title and body. Defaults to <feature-id>.
  --edit         Open the written goal.md in $VISUAL (or $EDITOR) after
                 writing it, then re-stamp the revision from the edited
                 content. Composes with --from to edit the adopted draft.
`

type goalIssueSource struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// runGoalInit implements `forge goal init <feature-id> [--force] [--from <path>] [--from-issue [<n>]] [--edit]`.
func runGoalInit(args []string) int {
	featureID, force, from, fromIssue, edit, code, done := parseGoalInitArgs(args)
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

	var goal *planning.Artifact
	if from != "" {
		src, err := os.ReadFile(from)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge goal init: reading %s: %v\n", from, err)
			return 1
		}
		goal = buildGoalFromSource(string(src))
	} else if fromIssue != "" {
		issue, err := fetchGoalIssueWithGH(context.Background(), fromIssue)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge goal init: fetching issue %s: %v\n", fromIssue, err)
			return 1
		}
		goal = buildGoalFromSource(buildGoalIssueSource(issue))
	} else {
		goal = buildGoalSkeleton()
	}

	ctx := context.Background()
	loader := &fileArtifactLoader{featureID: featureID}
	if err := loader.SaveGoal(ctx, featureID, goal); err != nil {
		fmt.Fprintf(os.Stderr, "forge goal init: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "wrote %s\n", path)

	if !edit {
		return 0
	}

	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		fmt.Fprintln(os.Stderr, "forge goal init: --edit requires $VISUAL or $EDITOR to be set")
		return 1
	}

	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "forge goal init: editor %q failed: %v\n", editor, err)
		return 1
	}

	edited, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge goal init: reading edited %s: %v\n", path, err)
		return 1
	}
	parsed, err := planning.Parse(edited)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge goal init: %s failed to parse after editing, left as saved: %v\n", path, err)
		return 1
	}
	parsed.Revision = planning.ComputeRevision(parsed)
	if err := loader.SaveGoal(ctx, featureID, parsed); err != nil {
		fmt.Fprintf(os.Stderr, "forge goal init: re-stamping %s: %v\n", path, err)
		return 1
	}

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

// buildGoalFromSource wraps a freeform doc's content in a fresh draft goal.md
// Artifact: the content is split into `##` sections (content before the
// first heading becomes a leading Section with an empty Heading), and a
// Revision is stamped from that content so the file is never Stale as
// written. If the source has no `##` headings at all, its full content is
// placed under a single "Goal" section rather than left as an unlabeled
// leading Section.
func buildGoalFromSource(src string) *planning.Artifact {
	sections := planning.ParseSections(src)
	if len(sections) == 1 && sections[0].Heading == "" {
		sections[0].Heading = "Goal"
	}

	a := &planning.Artifact{
		Kind:     planning.KindGoal,
		State:    "draft",
		Sections: sections,
	}
	a.Revision = planning.ComputeRevision(a)
	return a
}

func buildGoalIssueSource(issue goalIssueSource) string {
	title := strings.TrimSpace(issue.Title)
	body := strings.TrimSpace(issue.Body)
	if title == "" {
		return body
	}
	if body == "" {
		return "## Goal\n\n" + title + "\n"
	}
	return "## Goal\n\n" + title + "\n\n" + body + "\n"
}

func fetchGoalIssueWithGH(ctx context.Context, issueID string) (goalIssueSource, error) {
	cmd := exec.CommandContext(ctx, "gh", "issue", "view", issueID, "--json", "title,body")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return goalIssueSource{}, err
		}
		return goalIssueSource{}, fmt.Errorf("%w: %s", err, msg)
	}

	var issue goalIssueSource
	if err := json.Unmarshal(out, &issue); err != nil {
		return goalIssueSource{}, fmt.Errorf("parsing gh issue view output: %w", err)
	}
	return issue, nil
}

// parseGoalInitArgs parses `forge goal init <feature-id> [--force] [--from
// <path>] [--from-issue [<n>]] [--edit]`'s arguments. done is true when runGoalInit should return
// immediately with code (help text, or a parse error).
func parseGoalInitArgs(args []string) (featureID string, force bool, from string, fromIssue string, edit bool, code int, done bool) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(os.Stdout, goalUsage)
		return "", false, "", "", false, 0, true
	}

	if args[0] != "init" {
		fmt.Fprintf(os.Stderr, "forge goal: unknown subcommand %q\n\n%s", args[0], goalUsage)
		return "", false, "", "", false, 1, true
	}

	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch a {
		case "--help", "-h":
			fmt.Fprint(os.Stdout, goalUsage)
			return "", false, "", "", false, 0, true
		case "--force":
			force = true
		case "--from":
			if i+1 >= len(rest) {
				fmt.Fprintf(os.Stderr, "--from requires a <path> argument\n\n%s", goalUsage)
				return "", false, "", "", false, 1, true
			}
			i++
			from = rest[i]
		case "--from-issue":
			fromIssue = " "
			if i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "-") {
				i++
				fromIssue = rest[i]
			}
		case "--edit":
			edit = true
		default:
			if featureID != "" {
				fmt.Fprintf(os.Stderr, "too many arguments: %v\n\n%s", rest, goalUsage)
				return "", false, "", "", false, 1, true
			}
			featureID = a
		}
	}
	if featureID == "" {
		fmt.Fprintf(os.Stderr, "feature-id is required\n\n%s", goalUsage)
		return "", false, "", "", false, 1, true
	}
	if from != "" && fromIssue != "" {
		fmt.Fprintf(os.Stderr, "--from and --from-issue cannot be used together\n\n%s", goalUsage)
		return "", false, "", "", false, 1, true
	}
	if fromIssue == " " {
		fromIssue = featureID
	}
	return featureID, force, from, fromIssue, edit, 0, false
}

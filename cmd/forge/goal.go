package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningfs"
	"github.com/Teagan42/forge/internal/tracker"
)

const goalUsage = `Usage:
  forge goal init <feature-id> [--force] [--from <path>] [--from-issue [<n>]] [--edit]
  forge goal init --create-issue --title <title> [--from <path>] [--force] [--edit]

Create .forge/features/<feature-id>/goal.md, the human-authored Planning
Artifact that seeds 'forge plan'. The generated file is a skeleton with
placeholder prose under four sections (Goal, Context, Constraints, Success
Criteria) for the author to fill in, already stamped with a valid content
revision so it is not Stale.

A Feature is a local artifact by default: 'forge materialize' creates the
tracker issues from the ticket plan later. To make a Feature tracker-backed
now (so a planning needs-human pause posts to a real issue), give it the
issue number as its feature-id, seed it with --from-issue, or create the
issue with --create-issue.

  --force        Overwrite an existing goal.md and re-stamp a fresh draft.
  --from <path>  Adopt an existing freeform doc as the goal instead of
                 scaffolding a blank skeleton: its content is split into
                 '##' sections (content before the first heading becomes a
                 leading section; a doc with no headings becomes a single
                 'Goal' section) and stamped as a fresh draft.
  --from-issue [<n>]
                 Fetch issue <n> from the configured tracker and seed the
                 goal from its title and body. Defaults to <feature-id>, so
                 'forge goal init <n> --from-issue' makes a tracker-backed
                 Feature whose id is the issue number.
  --create-issue Create a new tracker issue from --title (and --from, if
                 given), then scaffold the goal under the created issue's id.
                 The Feature is tracker-backed. Requires --title; do not pass
                 a feature-id (it is the created issue's id).
  --title <t>    The title for the issue --create-issue creates.
  --edit         Open the written goal.md in $VISUAL (or $EDITOR) after
                 writing it, then re-stamp the revision from the edited
                 content. Composes with --from to edit the adopted draft.
`

type goalIssueSource struct {
	Title string
	Body  string
}

// goalTracker is the slice of the configured Tracker the goal command needs:
// reading an issue to seed a goal (--from-issue) and creating one from a goal
// (--create-issue).
type goalTracker interface {
	GetIssue(ctx context.Context, id string) (domain.Issue, error)
	CreateIssue(ctx context.Context, req tracker.IssueRequest) (tracker.CreatedIssue, error)
}

// newGoalTracker builds the configured Tracker for repoRoot. It is a var so a
// test injects a double without a real tracker or a .forge.yaml.
var newGoalTracker = func(repoRoot string) (goalTracker, error) {
	cfg, err := loadConfig(filepath.Join(repoRoot, defaultConfigPath))
	if err != nil {
		return nil, err
	}
	return buildTracker(cfg, repoRoot)
}

// goalInitOpts holds the parsed `forge goal init` flags.
type goalInitOpts struct {
	featureID   string
	force       bool
	from        string
	fromIssue   string
	edit        bool
	createIssue bool
	title       string
}

// runGoalInit implements `forge goal init`.
func runGoalInit(args []string) int {
	opts, code, done := parseGoalInitArgs(args)
	if done {
		return code
	}

	repoRoot, err := discoverRepoRootOrCWD()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge goal init: %v\n", err)
		return 1
	}

	ctx := context.Background()

	featureID, goal, code, done := resolveGoalSource(ctx, repoRoot, opts)
	if done {
		return code
	}

	path := filepath.Join(planningfs.FeatureDir(repoRoot, featureID), "goal.md")
	if !opts.force {
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintf(os.Stderr, "forge goal init: %s already exists; rerun with --force to overwrite\n", path)
			return 1
		} else if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "forge goal init: stat %s: %v\n", path, err)
			return 1
		}
	}

	loader := &fileArtifactLoader{RepoRoot: repoRoot}
	if err := loader.SaveGoal(ctx, featureID, goal); err != nil {
		fmt.Fprintf(os.Stderr, "forge goal init: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "wrote %s\n", path)

	if !opts.edit {
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

// resolveGoalSource resolves the feature-id and the goal Artifact from the
// parsed flags: it reads --from, fetches --from-issue from the configured
// tracker, creates the issue for --create-issue, or scaffolds a skeleton. done
// is true when runGoalInit should return immediately with code.
func resolveGoalSource(ctx context.Context, repoRoot string, opts goalInitOpts) (featureID string, goal *planning.Artifact, code int, done bool) {
	switch {
	case opts.createIssue:
		body := ""
		if opts.from != "" {
			src, err := os.ReadFile(opts.from)
			if err != nil {
				fmt.Fprintf(os.Stderr, "forge goal init: reading %s: %v\n", opts.from, err)
				return "", nil, 1, true
			}
			body = string(src)
		}
		trk, err := newGoalTracker(repoRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge goal init: %v\n", err)
			return "", nil, 1, true
		}
		created, err := trk.CreateIssue(ctx, tracker.IssueRequest{Title: opts.title, Body: body})
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge goal init: creating issue: %v\n", err)
			return "", nil, 1, true
		}
		featureID = created.ID
		if err := validateFeatureID(featureID); err != nil {
			fmt.Fprintf(os.Stderr, "forge goal init: created issue id %q is not a usable feature-id: %v\n", featureID, err)
			return "", nil, 1, true
		}
		source := body
		if strings.TrimSpace(source) == "" {
			source = buildGoalIssueSource(goalIssueSource{Title: opts.title})
		}
		fmt.Fprintf(os.Stdout, "created issue %s (%s)\n", featureID, created.URL)
		return featureID, buildGoalFromSource(source), 0, false

	case opts.fromIssue != "":
		if err := validateFeatureID(opts.featureID); err != nil {
			fmt.Fprintf(os.Stderr, "forge goal init: %v\n", err)
			return "", nil, 1, true
		}
		trk, err := newGoalTracker(repoRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge goal init: %v\n", err)
			return "", nil, 1, true
		}
		issue, err := trk.GetIssue(ctx, opts.fromIssue)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge goal init: fetching issue %s: %v\n", opts.fromIssue, err)
			return "", nil, 1, true
		}
		source := buildGoalIssueSource(goalIssueSource{Title: issue.Title, Body: issue.Body})
		return opts.featureID, buildGoalFromSource(source), 0, false

	case opts.from != "":
		if err := validateFeatureID(opts.featureID); err != nil {
			fmt.Fprintf(os.Stderr, "forge goal init: %v\n", err)
			return "", nil, 1, true
		}
		src, err := os.ReadFile(opts.from)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge goal init: reading %s: %v\n", opts.from, err)
			return "", nil, 1, true
		}
		return opts.featureID, buildGoalFromSource(string(src)), 0, false

	default:
		if err := validateFeatureID(opts.featureID); err != nil {
			fmt.Fprintf(os.Stderr, "forge goal init: %v\n", err)
			return "", nil, 1, true
		}
		return opts.featureID, buildGoalSkeleton(), 0, false
	}
}

// parseGoalInitArgs parses `forge goal init`'s arguments. done is true when
// runGoalInit should return immediately with code (help text, or a parse
// error).
func parseGoalInitArgs(args []string) (opts goalInitOpts, code int, done bool) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(os.Stdout, goalUsage)
		return goalInitOpts{}, 0, true
	}

	if args[0] != "init" {
		fmt.Fprintf(os.Stderr, "forge goal: unknown subcommand %q\n\n%s", args[0], goalUsage)
		return goalInitOpts{}, 1, true
	}

	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch a {
		case "--help", "-h":
			fmt.Fprint(os.Stdout, goalUsage)
			return goalInitOpts{}, 0, true
		case "--force":
			opts.force = true
		case "--from":
			if i+1 >= len(rest) {
				fmt.Fprintf(os.Stderr, "--from requires a <path> argument\n\n%s", goalUsage)
				return goalInitOpts{}, 1, true
			}
			i++
			opts.from = rest[i]
		case "--from-issue":
			opts.fromIssue = " "
			if i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "-") {
				i++
				opts.fromIssue = rest[i]
			}
		case "--create-issue":
			opts.createIssue = true
		case "--title":
			if i+1 >= len(rest) {
				fmt.Fprintf(os.Stderr, "--title requires a <title> argument\n\n%s", goalUsage)
				return goalInitOpts{}, 1, true
			}
			i++
			opts.title = rest[i]
		case "--edit":
			opts.edit = true
		default:
			if opts.featureID != "" {
				fmt.Fprintf(os.Stderr, "too many arguments: %v\n\n%s", rest, goalUsage)
				return goalInitOpts{}, 1, true
			}
			opts.featureID = a
		}
	}

	if opts.from != "" && opts.fromIssue != "" {
		fmt.Fprintf(os.Stderr, "--from and --from-issue cannot be used together\n\n%s", goalUsage)
		return goalInitOpts{}, 1, true
	}

	if opts.createIssue {
		if opts.fromIssue != "" {
			fmt.Fprintf(os.Stderr, "--create-issue and --from-issue cannot be used together\n\n%s", goalUsage)
			return goalInitOpts{}, 1, true
		}
		if strings.TrimSpace(opts.title) == "" {
			fmt.Fprintf(os.Stderr, "--create-issue requires --title\n\n%s", goalUsage)
			return goalInitOpts{}, 1, true
		}
		if opts.featureID != "" {
			fmt.Fprintf(os.Stderr, "do not pass a feature-id with --create-issue; it is the created issue's id\n\n%s", goalUsage)
			return goalInitOpts{}, 1, true
		}
		return opts, 0, false
	}

	if opts.title != "" {
		fmt.Fprintf(os.Stderr, "--title is only valid with --create-issue\n\n%s", goalUsage)
		return goalInitOpts{}, 1, true
	}
	if opts.featureID == "" {
		fmt.Fprintf(os.Stderr, "feature-id is required\n\n%s", goalUsage)
		return goalInitOpts{}, 1, true
	}
	if opts.fromIssue == " " {
		opts.fromIssue = opts.featureID
	}
	return opts, 0, false
}

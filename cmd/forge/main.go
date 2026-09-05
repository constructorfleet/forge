// Command forge is Forge's CLI entrypoint. Subcommands land incrementally
// across tickets: `forge init` (ticket 29) generates .forge.yaml, and
// `forge execute`/`forge status` (ticket 18) drive and inspect an Execution.
// `forge plan` (ticket 16) drives the planning compiler pipeline.
// `forge approve` (ticket 17) approves a Specification at a specific revision.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Teagan42/forge/internal/initdiscovery"
	"github.com/Teagan42/forge/internal/planningapprove"
	"github.com/Teagan42/forge/internal/planningfs"
	"github.com/Teagan42/forge/internal/storage"
)

// fileArtifactLoader is cmd/forge's alias for the reusable filesystem
// Planning Artifact loader (internal/planningfs), so the many existing call
// sites in this package need no rename.
type fileArtifactLoader = planningfs.FileArtifactLoader

const helpText = `forge - deterministic orchestration for software-engineering agents

Usage:
  forge [command]

Commands:
  init                     Generate .forge.yaml via deterministic repository-policy discovery
  execute <issue-number>   Execute a single Issue with no unmet Dependencies
  status [execution-id]    Show active Executions or one Execution's persisted state
  watch [execution-id]     Attach the live roster to an active Execution
  cancel <execution-id>    Stop an active Execution and mark running work CANCELLED
  retry <execution>/<issue> Retry a FAILED Issue within its Execution
  resume <execution-id>    Reconcile and continue an incomplete Execution
  goal init <feature-id>   Create a skeleton .forge/features/<feature-id>/goal.md
  plan <feature-id>        Run the planning compiler pipeline for a Feature
  approve <feature-id> spec   Approve a Specification at its current revision
  approve <feature-id> tickets  Approve a Ticket Plan at its current revision
  materialize <feature-id> Turn an approved Ticket Plan into an executable Issue DAG
  internal-mcp --workspace <path>  Start the semantic-navigation MCP server (spawned by Agent backends, not run interactively)
  version                  Print the build's embedded commit SHA and build time
  help                     Show this help text

Run 'forge <command> --help' for command-specific flags.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprint(os.Stdout, helpText)
		return 0
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "init":
		if err := runInit(rest); err != nil {
			fmt.Fprintf(os.Stderr, "forge init: %v\n", err)
			return 1
		}
		return 0
	case "execute":
		return runExecute(rest)
	case "status":
		return runStatus(rest)
	case "watch":
		return runWatch(rest)
	case "cancel":
		return runCancel(rest)
	case "retry":
		return runRetry(rest)
	case "resume":
		return runResume(rest)
	case "goal":
		return runGoalInit(rest)
	case "plan":
		return runPlan(rest)
	case "materialize":
		return runMaterialize(rest)
	case "internal-mcp":
		return runInternalMCP(rest)
	case "version":
		return runVersion(rest)
	case "approve":
		if len(rest) >= 2 && rest[1] == "tickets" {
			return runApproveTickets(rest)
		}
		return runApprove(rest)
	default:
		fmt.Fprintf(os.Stderr, "forge: unknown command %q\n\n", cmd)
		fmt.Fprint(os.Stderr, helpText)
		return 1
	}
}

const initUsage = `Usage: forge init [--force] [dir]

Generate .forge.yaml via deterministic repository-policy discovery.
dir defaults to the current directory.

  --force   Overwrite an existing .forge.yaml instead of refusing to.
`

func runInit(args []string) error {
	dir := "."
	force := false
	var positional []string
	for _, a := range args {
		switch a {
		case "--help", "-h":
			fmt.Fprint(os.Stdout, initUsage)
			return nil
		case "--force":
			force = true
		default:
			positional = append(positional, a)
		}
	}
	switch len(positional) {
	case 0:
	case 1:
		dir = positional[0]
	default:
		return fmt.Errorf("too many arguments: %v\n\n%s", positional, initUsage)
	}

	path := filepath.Join(dir, ".forge.yaml")
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; rerun with --force to overwrite", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}

	result := initdiscovery.Detect(dir)

	out, err := initdiscovery.Render(result)
	if err != nil {
		return fmt.Errorf("render .forge.yaml: %w", err)
	}

	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	fmt.Fprintf(os.Stdout, "wrote %s\n", path)
	for _, n := range result.Notes {
		fmt.Fprintf(os.Stdout, "  note: %s: %s\n", n.Field, n.Message)
	}
	return nil
}

const approveUsage = `Usage: forge approve <feature-id> spec

Approve a Specification at its current revision. The approval binds to the
specification's content revision (computed from Kind, DerivedFrom, and Sections).
If the specification's definitional content is edited, its revision changes and
the approval is automatically invalidated.

This command requires that the Specification has passed automated review
(VerdictApproved from SpecificationReview).
`

func runApprove(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(os.Stdout, approveUsage)
		return 0
	}

	if len(args) < 2 || args[1] != "spec" {
		fmt.Fprintf(os.Stderr, "forge approve: expected 'spec' as second argument\n\n%s", approveUsage)
		return 1
	}

	featureID := args[0]

	ctx := context.Background()

	dsn := filepath.Join(".forge", "forge.db")
	store, err := storage.Open(dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage.Open: %v\n", err)
		return 1
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Migrate: %v\n", err)
		return 1
	}

	approver := &planningapprove.Approver{Store: store, Artifacts: &fileArtifactLoader{}}

	currentRev, err := approver.ApproveSpec(ctx, featureID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge approve: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "spec.md approved for feature %s at revision %s\n", featureID, currentRev[:16])
	return 0
}

const approveTicketsUsage = `Usage: forge approve <feature-id> tickets

Approve a Ticket Plan at its current revision. The approval binds to the
ticket plan's content revision (computed from Kind, DerivedFrom, and Sections).
If the ticket plan's definitional content is edited, its revision changes and
the approval is automatically invalidated.

This command requires that the Ticket Plan has passed automated review
(VerdictApproved from TicketPlanReview).
`

func runApproveTickets(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(os.Stdout, approveTicketsUsage)
		return 0
	}

	if len(args) < 2 || args[1] != "tickets" {
		fmt.Fprintf(os.Stderr, "forge approve: expected 'tickets' as second argument\n\n%s", approveTicketsUsage)
		return 1
	}

	featureID := args[0]

	ctx := context.Background()

	dsn := filepath.Join(".forge", "forge.db")
	store, err := storage.Open(dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage.Open: %v\n", err)
		return 1
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Migrate: %v\n", err)
		return 1
	}

	approver := &planningapprove.Approver{Store: store, Artifacts: &fileArtifactLoader{}}

	result, err := approver.ApproveTicketPlan(ctx, featureID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge approve tickets: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "ticket-plan.md approved for feature %s at revision %s\n", featureID, result.Revision[:16])

	if result.Resumed {
		for _, id := range result.Superseded {
			fmt.Fprintf(os.Stdout, "issue %s closed as superseded by the new ticket plan\n", id)
		}
		fmt.Fprintf(os.Stdout, "feature %s unfrozen; work may resume against the approved plan\n", featureID)
	}
	return 0
}

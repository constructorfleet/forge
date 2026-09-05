package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"

	"github.com/Teagan42/forge/internal/materialize"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/ticketplan"
)

const materializeUsage = `Usage: forge materialize <feature-id>

Turn an approved Ticket Plan into a valid, executable tracker Issue DAG
(see CONTEXT.md "Issue", "Dependency"). This is all-or-nothing: Issues are
created in a non-executable state, their Dependencies and provenance are
stamped, and the whole resulting graph is re-fetched and validated before
any Issue becomes executable. A partial failure leaves the created Issues
permanently non-executable rather than runnable by 'forge execute'.

This command requires that the Ticket Plan (and the Specification it was
derived from) have already been approved via 'forge approve <feature-id>
spec' and 'forge approve <feature-id> tickets'.
`

// runMaterialize implements `forge materialize <feature-id>`, the bridge
// between Phase 2's planning compiler and Phase 1's execution engine (see
// internal/materialize's package doc comment for the Phase A/B/C
// breakdown).
func runMaterialize(args []string) int {
	fs := flag.NewFlagSet("forge materialize", flag.ContinueOnError)
	configPath := fs.String("config", defaultConfigPath, "path to .forge.yaml")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 || fs.Arg(0) == "--help" || fs.Arg(0) == "-h" {
		fmt.Fprint(os.Stdout, materializeUsage)
		return 0
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "forge materialize: expected exactly one argument, <feature-id>\n\n%s", materializeUsage)
		return 2
	}
	featureID := fs.Arg(0)

	repoRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge materialize: %v\n", err)
		return 1
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge materialize: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	loader := &fileArtifactLoader{}

	specArtifact, err := loader.LoadSpec(ctx, featureID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge materialize: load spec: %v\n", err)
		return 1
	}
	if specArtifact == nil {
		fmt.Fprintf(os.Stderr, "forge materialize: no spec found for feature %s\n", featureID)
		return 1
	}
	if !planning.Approved(specArtifact) {
		fmt.Fprintf(os.Stderr, "forge materialize: spec.md for feature %s is not approved at its current revision\n", featureID)
		return 1
	}

	tpArtifact, err := loader.LoadTicketPlan(ctx, featureID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge materialize: load ticket plan: %v\n", err)
		return 1
	}
	if tpArtifact == nil {
		fmt.Fprintf(os.Stderr, "forge materialize: no ticket plan found for feature %s\n", featureID)
		return 1
	}
	if !planning.Approved(tpArtifact) {
		fmt.Fprintf(os.Stderr, "forge materialize: ticket-plan.md for feature %s is not approved at its current revision\n", featureID)
		return 1
	}

	tickets, err := ticketplan.ParseTicketPlan(tpArtifact)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge materialize: parse ticket plan: %v\n", err)
		return 1
	}

	if err := verifyTrackerAuth(ctx, cfg, repoRoot); err != nil {
		fmt.Fprintf(os.Stderr, "forge materialize: %v\n", err)
		return 1
	}

	trk, err := buildTracker(cfg, repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge materialize: %v\n", err)
		return 1
	}

	opts := materialize.Options{
		Project:      featureID,
		SpecRevision: specArtifact.Revision,
		PlanRevision: tpArtifact.Revision,
		Decisions:    relevantDecisions(specArtifact),
	}

	result, err := materialize.Materialize(ctx, trk, tickets, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge materialize: %v\n", err)
		return 1
	}

	keys := make([]string, 0, len(result.IssueIDs))
	for key := range result.IssueIDs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Printf("%s -> issue %s\n", key, result.IssueIDs[key])
	}
	manualKeys := make([]string, 0, len(result.ManualIssueIDs))
	for key := range result.ManualIssueIDs {
		manualKeys = append(manualKeys, key)
	}
	sort.Strings(manualKeys)
	for _, key := range manualKeys {
		fmt.Printf("%s -> manual issue %s\n", key, result.ManualIssueIDs[key])
	}
	return 0
}

// relevantDecisions returns the Decision artifact IDs the Specification
// was derived from, deduplicated and sorted, for stamping onto every
// materialized Issue (see materialize.Options.Decisions).
func relevantDecisions(spec *planning.Artifact) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, d := range spec.DerivedFrom {
		if d.Kind != planning.KindDecision || seen[d.ID] {
			continue
		}
		seen[d.ID] = true
		ids = append(ids, d.ID)
	}
	sort.Strings(ids)
	return ids
}

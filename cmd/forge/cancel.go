package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/Teagan42/forge/internal/engine"
)

func runCancel(args []string) int {
	fs := flag.NewFlagSet("forge cancel", flag.ContinueOnError)
	configPath := fs.String("config", defaultConfigPath, "path to .forge.yaml")
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite state database")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "forge cancel: expected exactly one argument, <execution-id>")
		return 2
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge cancel: %v\n", err)
		return 1
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge cancel: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store, err := openStore(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge cancel: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	eng := buildOperationalEngine(store, cfg, repoRoot)
	state, err := eng.CancelExecution(ctx, fs.Arg(0))
	if err != nil {
		// An unresponsive worker owner is a warning: the Issues are
		// cancelled and the state below is valid.
		var ownerErr *engine.CancelOwnerError
		if !errors.As(err, &ownerErr) {
			fmt.Fprintf(os.Stderr, "forge cancel: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "forge cancel: warning: %v\n", ownerErr)
	}

	fmt.Printf("execution %s cancelled\n", state.Execution.ID)
	for _, issue := range state.Issues {
		fmt.Printf("  issue %s -> %s\n", issue.ID, issue.State)
	}
	return 0
}

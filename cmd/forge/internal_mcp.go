package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/Teagan42/forge/internal/lsp"
	"github.com/Teagan42/forge/internal/semantic/lspdriver"
	"github.com/Teagan42/forge/internal/semantic/mcpserver"
	"github.com/Teagan42/forge/internal/semantic/toolsurface"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// runInternalMCP implements `forge internal-mcp --workspace <path>`: a
// backend-neutral Model Context Protocol server exposing Forge's
// semantic-navigation tool surface (internal/semantic/toolsurface) backed
// by a Forge-managed LSP driver (internal/semantic/lspdriver) rooted at
// <path>, served over stdio.
//
// It is not meant to be run interactively — an Agent backend consuming
// semantic navigation over the "mcp" Injection Channel (see CONTEXT.md)
// spawns and owns this subprocess itself (issue #127; the Codex adapter is
// the v1 caller).
func runInternalMCP(args []string) int {
	fs := flag.NewFlagSet("forge internal-mcp", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "path to the workspace gopls should index (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *workspace == "" {
		fmt.Fprintln(os.Stderr, "forge internal-mcp: --workspace is required")
		return 2
	}

	info, err := os.Stat(*workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge internal-mcp: workspace %q: %v\n", *workspace, err)
		return 1
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "forge internal-mcp: workspace %q is not a directory\n", *workspace)
		return 1
	}

	cfg, err := loadConfig(filepath.Join(*workspace, defaultConfigPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge internal-mcp: %v\n", err)
		return 1
	}

	registry := lsp.NewRegistry(cfg.LSP)
	spec, ok := registry["go"]
	if !ok {
		fmt.Fprintln(os.Stderr, "forge internal-mcp: no gopls entry in the Language Server Registry")
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	driver := lspdriver.New(lspdriver.Options{
		Command:          spec.Command,
		Dir:              *workspace,
		ReadinessTimeout: cfg.LSP.ReadinessTimeout,
		RestartLimit:     cfg.LSP.RestartLimit,
	})
	driver.Start(ctx)
	defer func() { _ = driver.Shutdown(context.Background()) }()

	toolset := toolsurface.NewToolset(driver, toolsurface.Options{MaxResults: cfg.LSP.MaxResults})
	server := mcpserver.New(toolset)

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "forge internal-mcp: %v\n", err)
		return 1
	}
	return 0
}

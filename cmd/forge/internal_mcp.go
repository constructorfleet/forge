package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/lsp"
	"github.com/Teagan42/forge/internal/repocontext"
	"github.com/Teagan42/forge/internal/semantic/lspdriver"
	"github.com/Teagan42/forge/internal/semantic/mcpserver"
	"github.com/Teagan42/forge/internal/semantic/toolsurface"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// runInternalMCP implements `forge internal-mcp --workspace <path>`: a
// backend-neutral Model Context Protocol server exposing Forge's
// semantic-navigation tool surface (internal/semantic/toolsurface) over
// stdio, rooted at <path>.
//
// It is a multiplexer (ADR 0016): it detects the workspace's languages, maps
// them through the Language Server Registry, starts one Forge-managed LSP
// driver (internal/semantic/lspdriver) per Detected Server, and serves a
// single, un-namespaced tool set over all of them — a toolsurface.Router
// dispatches each call to the driver serving the file argument's extension
// and fans workspace-symbol searches out across every driver.
//
// It is not meant to be run interactively — an Agent backend consuming
// semantic navigation over the "mcp" Injection Channel (see CONTEXT.md)
// spawns and owns this subprocess itself (issue #127; the Codex adapter is
// the v1 caller).
func runInternalMCP(args []string) int {
	fs := flag.NewFlagSet("forge internal-mcp", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "path to the workspace the language servers should index (required)")
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

	servers, err := detectWorkspaceServers(cfg, *workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge internal-mcp: %v\n", err)
		return 1
	}
	if len(servers) == 0 {
		// Fail fast rather than serving an empty tool set: with no server
		// to multiplex there is nothing to serve, and a silent no-op
		// endpoint would look to the agent like a repository with no
		// symbols in it.
		fmt.Fprintf(os.Stderr, "forge internal-mcp: no Language Server Registry entry for any language detected in %q\n", *workspace)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	drivers, shutdown := startDrivers(ctx, *workspace, cfg.LSP, servers)
	defer shutdown()

	router := toolsurface.NewRouter(drivers, lsp.Extensions(cfg.LSP))
	toolset := toolsurface.NewToolset(router, toolsurface.Options{MaxResults: cfg.LSP.MaxResults})
	server := mcpserver.New(toolset)

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "forge internal-mcp: %v\n", err)
		return 1
	}
	return 0
}

// detectWorkspaceServers maps the workspace's detected languages through the
// Language Server Registry built from cfg. It reuses repocontext's manifest
// detection — the same language signal the Engine's semantic path uses (see
// internal/engine/semantic.go) — so this subcommand and the descriptor that
// asked for it agree on what the workspace is written in.
func detectWorkspaceServers(cfg config.Config, workspace string) ([]lsp.DetectedServer, error) {
	repoCtx, err := repocontext.Compile(cfg, workspace, "")
	if err != nil {
		return nil, err
	}
	return lsp.Detect(repoCtx.Languages, lsp.NewRegistry(cfg.LSP)), nil
}

// startDrivers starts one driver per Detected Server, keyed by language for
// the Router, and returns the func that shuts every one of them down.
//
// A server that cannot be launched or does not complete its handshake is
// left inert by the driver itself rather than reported here: per ADR 0016 a
// missing language server degrades that one language, never the whole tool
// surface.
func startDrivers(ctx context.Context, workspace string, cfg config.LSPConfig, servers []lsp.DetectedServer) (map[string]toolsurface.Driver, func()) {
	drivers := make(map[string]toolsurface.Driver, len(servers))
	started := make([]*lspdriver.Driver, 0, len(servers))

	for _, server := range servers {
		driver := lspdriver.New(driverOptions(workspace, cfg, server))
		driver.Start(ctx)
		drivers[server.Language] = driver
		started = append(started, driver)
	}

	return drivers, func() {
		for _, driver := range started {
			_ = driver.Shutdown(context.Background())
		}
	}
}

// driverOptions builds one Detected Server's driver options, carrying the
// server's registry ServerProfile through so each language server gets the
// init options, hover shape, and symbol-children handling its quirks need.
func driverOptions(workspace string, cfg config.LSPConfig, server lsp.DetectedServer) lspdriver.Options {
	return lspdriver.Options{
		Command:          server.Command,
		Dir:              workspace,
		ReadinessTimeout: cfg.ReadinessTimeout,
		RestartLimit:     cfg.RestartLimit,
		Profile:          server.Profile,
	}
}

package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
)

// lspPluginDirName is the fixed, session-only directory Execute writes the
// Claude Code plugin into under the Issue's Workspace. It is created fresh
// and removed again within a single Execute call, so its name never needs
// to be unique across invocations — a Workspace has at most one Agent call
// running against it at a time.
const lspPluginDirName = ".forge-claude-lsp-plugin"

// languageFileExtensions maps a Detected Server's Language (as populated by
// the Language Server Registry, internal/lsp) to the file extension Claude
// Code's `.lsp.json` uses to route files to it. v1 has exactly one entry,
// matching the registry's only built-in server.
var languageFileExtensions = map[string]string{
	"go": ".go",
}

// lspPluginManifest is the minimal Claude Code plugin manifest
// (`.claude-plugin/plugin.json`) Forge writes to make a `--plugin-dir`
// entry a valid plugin.
type lspPluginManifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// lspServerEntry is one `.lsp.json` entry: the command Claude Code's native
// `LSP` tool should launch, and which file extensions route to it.
type lspServerEntry struct {
	Command             string            `json:"command"`
	ExtensionToLanguage map[string]string `json:"extensionToLanguage"`
}

// buildLSPServerConfig translates servers (the SemanticProvider seam's
// backend-neutral NativeServer identities) into `.lsp.json`'s shape, keyed
// by each server's command basename (e.g. "gopls"). A server whose Language
// has no known file extension (languageFileExtensions) is skipped — Claude
// Code's `.lsp.json` has no way to route files to it without one.
func buildLSPServerConfig(servers []agent.NativeServer) map[string]lspServerEntry {
	config := make(map[string]lspServerEntry, len(servers))
	for _, server := range servers {
		if len(server.Command) == 0 {
			continue
		}
		ext, ok := languageFileExtensions[strings.ToLower(server.Language)]
		if !ok {
			continue
		}
		name := filepath.Base(server.Command[0])
		config[name] = lspServerEntry{
			Command:             strings.Join(server.Command, " "),
			ExtensionToLanguage: map[string]string{ext: strings.ToLower(server.Language)},
		}
	}
	return config
}

// writeLSPPlugin writes a per-invocation, session-only Claude Code plugin
// into workspacePath for servers (see CONTEXT.md "Injection Channel":
// InjectionChannelLSPPlugin) — a `.claude-plugin/plugin.json` manifest plus
// a `.lsp.json` naming each server, so Claude Code's native `LSP` tool
// auto-enables against it once `--plugin-dir` points there.
//
// Returns ("", a no-op cleanup, nil) when servers yields no fillable entry
// (e.g. every server's Language lacks a known file extension) — Execute
// treats that the same as no NativeServers at all, appending no
// `--plugin-dir` flag. cleanup removes whatever was written and must be
// called (via defer) once the invocation using it is done, keeping the
// plugin session-only rather than a persistent addition to the Workspace.
func writeLSPPlugin(workspacePath string, servers []agent.NativeServer) (pluginDir string, cleanup func(), err error) {
	noop := func() {}

	entries := buildLSPServerConfig(servers)
	if len(entries) == 0 {
		return "", noop, nil
	}

	pluginDir = filepath.Join(workspacePath, lspPluginDirName)
	manifestDir := filepath.Join(pluginDir, ".claude-plugin")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		return "", noop, fmt.Errorf("claude adapter: create lsp plugin dir: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(pluginDir) }

	manifest := lspPluginManifest{
		Name:        "forge-lsp",
		Version:     "0.0.0",
		Description: "Forge-provisioned language servers for Claude Code's native LSP tool",
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		cleanup()
		return "", noop, fmt.Errorf("claude adapter: encode plugin manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), manifestBytes, 0o644); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("claude adapter: write plugin manifest: %w", err)
	}

	lspBytes, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		cleanup()
		return "", noop, fmt.Errorf("claude adapter: encode .lsp.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, ".lsp.json"), lspBytes, 0o644); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("claude adapter: write .lsp.json: %w", err)
	}

	return pluginDir, cleanup, nil
}

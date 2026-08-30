package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
)

// implementedStdout is the canned CLI output every test in this file uses:
// none of them care about result parsing, only about the CLI invocation
// (args) and the workspace's filesystem state around it.
const implementedStdout = "```json\n" + `{"status":"IMPLEMENTED","summary":"done"}` + "\n```\n"

// TestExecute_WithNativeServers_WritesLSPPluginAndAppendsPluginDirFlag is
// this ticket's (#128) core red test: when the SemanticProvider seam fills
// AgentRequest.Semantic.NativeServers with a gopls identity, the Claude
// adapter must write a session-only Claude Code plugin (a plugin manifest
// plus a `.lsp.json` naming the server) into the workspace, and point
// `--plugin-dir` at it — the mechanism by which Claude Code's native `LSP`
// tool auto-enables against it.
func TestExecute_WithNativeServers_WritesLSPPluginAndAppendsPluginDirFlag(t *testing.T) {
	// The plugin directory is session-only: Execute removes it (via defer)
	// before returning, so its contents must be inspected from inside the
	// Runner call itself, while Execute is still in flight.
	var (
		manifestBytes  []byte
		manifestErr    error
		lspBytes       []byte
		lspErr         error
		observedInside string
	)
	runner := func(_ context.Context, _ string, args []string, _ string, _ []string, _ func(string)) (string, string, int, error) {
		observedInside = findFlagValue(t, args, "--plugin-dir")
		manifestBytes, manifestErr = os.ReadFile(filepath.Join(observedInside, ".claude-plugin", "plugin.json"))
		lspBytes, lspErr = os.ReadFile(filepath.Join(observedInside, ".lsp.json"))
		return implementedStdout, "", 0, nil
	}
	a := &Adapter{Runner: runner}

	req := baseRequest()
	req.WorkspacePath = t.TempDir()
	req.Semantic.NativeServers = []agent.NativeServer{{Language: "go", Command: []string{"gopls"}}}

	if _, err := a.Execute(t.Context(), req); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !isWithin(observedInside, req.WorkspacePath) {
		t.Fatalf("--plugin-dir %q is not inside the workspace %q", observedInside, req.WorkspacePath)
	}

	if manifestErr != nil {
		t.Fatalf("read .claude-plugin/plugin.json: %v", manifestErr)
	}
	var manifest struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf(".claude-plugin/plugin.json is not valid JSON: %v", err)
	}
	if manifest.Name == "" {
		t.Fatalf("plugin.json missing a name: %s", manifestBytes)
	}

	if lspErr != nil {
		t.Fatalf("read .lsp.json: %v", lspErr)
	}
	var lspConfig map[string]struct {
		Command             string            `json:"command"`
		ExtensionToLanguage map[string]string `json:"extensionToLanguage"`
	}
	if err := json.Unmarshal(lspBytes, &lspConfig); err != nil {
		t.Fatalf(".lsp.json is not valid JSON: %v", err)
	}
	gopls, ok := lspConfig["gopls"]
	if !ok {
		t.Fatalf(".lsp.json = %s, want a \"gopls\" entry", lspBytes)
	}
	if gopls.Command != "gopls" {
		t.Errorf("gopls.command = %q, want %q", gopls.Command, "gopls")
	}
	if gopls.ExtensionToLanguage[".go"] != "go" {
		t.Errorf("gopls.extensionToLanguage = %+v, want {\".go\": \"go\"}", gopls.ExtensionToLanguage)
	}
}

// TestBuildLSPServerConfig_PythonServer_SplitsCommandAndArgsAndMapsExtensions
// is this ticket's (#154) core red test for the command/args split bug:
// pyright-langserver's registry Command is multi-token
// (["pyright-langserver", "--stdio"]); pre-#154 buildLSPServerConfig joined
// it into one non-PATH-resolvable "command" string. It must instead emit
// the bare binary as "command" and the rest as "args", plus the python
// extensionToLanguage entries (.py, .pyi) from the adapter-local table.
func TestBuildLSPServerConfig_PythonServer_SplitsCommandAndArgsAndMapsExtensions(t *testing.T) {
	config := buildLSPServerConfig([]agent.NativeServer{
		{Language: "python", Command: []string{"pyright-langserver", "--stdio"}},
	})

	entry, ok := config["pyright-langserver"]
	if !ok {
		t.Fatalf("config = %+v, want a %q entry", config, "pyright-langserver")
	}
	if entry.Command != "pyright-langserver" {
		t.Errorf("Command = %q, want %q", entry.Command, "pyright-langserver")
	}
	if len(entry.Args) != 1 || entry.Args[0] != "--stdio" {
		t.Errorf("Args = %+v, want [\"--stdio\"]", entry.Args)
	}
	want := map[string]string{".py": "python", ".pyi": "python"}
	if len(entry.ExtensionToLanguage) != len(want) {
		t.Errorf("ExtensionToLanguage = %+v, want %+v", entry.ExtensionToLanguage, want)
	}
	for ext, lang := range want {
		if entry.ExtensionToLanguage[ext] != lang {
			t.Errorf("ExtensionToLanguage[%q] = %q, want %q", ext, entry.ExtensionToLanguage[ext], lang)
		}
	}
}

// TestBuildLSPServerConfig_JavaScriptServer_MapsAnthropicMultiExtensionIDs
// covers the "javascript" registry key's full Anthropic .lsp.json language
// ID fan-out: .ts/.mts/.cts -> typescript, .tsx -> typescriptreact,
// .js/.mjs/.cjs -> javascript, .jsx -> javascriptreact.
func TestBuildLSPServerConfig_JavaScriptServer_MapsAnthropicMultiExtensionIDs(t *testing.T) {
	config := buildLSPServerConfig([]agent.NativeServer{
		{Language: "javascript", Command: []string{"typescript-language-server", "--stdio"}},
	})

	entry, ok := config["typescript-language-server"]
	if !ok {
		t.Fatalf("config = %+v, want a %q entry", config, "typescript-language-server")
	}
	want := map[string]string{
		".ts":  "typescript",
		".tsx": "typescriptreact",
		".mts": "typescript",
		".cts": "typescript",
		".js":  "javascript",
		".jsx": "javascriptreact",
		".mjs": "javascript",
		".cjs": "javascript",
	}
	if len(entry.ExtensionToLanguage) != len(want) {
		t.Errorf("ExtensionToLanguage = %+v, want %+v", entry.ExtensionToLanguage, want)
	}
	for ext, lang := range want {
		if entry.ExtensionToLanguage[ext] != lang {
			t.Errorf("ExtensionToLanguage[%q] = %q, want %q", ext, entry.ExtensionToLanguage[ext], lang)
		}
	}
}

// TestBuildLSPServerConfig_UnknownLanguage_SkipsServer asserts the skip
// rule change: a server is skipped only because the adapter has no
// extension table for its Language, not via any single-ext lookup.
func TestBuildLSPServerConfig_UnknownLanguage_SkipsServer(t *testing.T) {
	config := buildLSPServerConfig([]agent.NativeServer{
		{Language: "ruby", Command: []string{"solargraph", "stdio"}},
	})

	if len(config) != 0 {
		t.Errorf("config = %+v, want empty: \"ruby\" has no extensionToLanguage table entry", config)
	}
}

// isWithin reports whether target is a descendant of dir.
func isWithin(target, dir string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// TestExecute_WithNativeServers_CleansUpPluginDirAfterExecute asserts the
// plugin is session-only (per invocation): once Execute returns, nothing it
// wrote into the workspace remains, so it never pollutes the Issue's diff.
func TestExecute_WithNativeServers_CleansUpPluginDirAfterExecute(t *testing.T) {
	var calls []recordedCall
	a := &Adapter{Runner: newFakeRunner(&calls, implementedStdout, "", 0, nil)}

	req := baseRequest()
	req.WorkspacePath = t.TempDir()
	req.Semantic.NativeServers = []agent.NativeServer{{Language: "go", Command: []string{"gopls"}}}

	if _, err := a.Execute(t.Context(), req); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	pluginDir := findFlagValue(t, calls[0].args, "--plugin-dir")
	if _, err := os.Stat(pluginDir); !os.IsNotExist(err) {
		t.Fatalf("plugin dir %q still exists after Execute returned (err=%v), want it removed", pluginDir, err)
	}
}

// TestExecute_NoNativeServers_NoPluginDirFlag is the degradation case: an
// AgentRequest whose Semantic descriptor has no NativeServers (the zero
// value every pre-#128 request carried, and still what a non-Go workspace
// or a disabled `lsp` config produces) must not append --plugin-dir at all.
func TestExecute_NoNativeServers_NoPluginDirFlag(t *testing.T) {
	var calls []recordedCall
	a := &Adapter{Runner: newFakeRunner(&calls, implementedStdout, "", 0, nil)}

	if _, err := a.Execute(t.Context(), baseRequest()); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	for _, arg := range calls[0].args {
		if arg == "--plugin-dir" {
			t.Fatalf("args %v contain --plugin-dir, want none (no NativeServers)", calls[0].args)
		}
	}
}

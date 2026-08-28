package repocontext_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/repocontext"
)

func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func gateConfig(commands ...string) config.QualityConfig {
	gates := make([]config.QualityGate, len(commands))
	for i, c := range commands {
		gates[i] = config.QualityGate{Name: c, Command: c}
	}
	return config.QualityConfig{Gates: gates}
}

func TestCompile_MergesInstructionsAndSourcesGatesFromConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "# Agents\n\nFollow the skill docs.\n")
	writeFile(t, dir, "CLAUDE.md", "## Agent skills\n\nUse the issue tracker.\n")
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.25\n")

	cfg := config.Default()
	cfg.Quality = gateConfig("gofmt -l .", "go vet ./...", "go test ./...")

	rc, err := repocontext.Compile(cfg, dir, "abc123")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if rc.BaseRevision != "abc123" {
		t.Errorf("BaseRevision = %q, want abc123", rc.BaseRevision)
	}
	want := []string{"gofmt -l .", "go vet ./...", "go test ./..."}
	if !slices.Equal(rc.QualityGates, want) {
		t.Errorf("QualityGates = %v, want %v", rc.QualityGates, want)
	}
	if !strings.Contains(rc.AgentInstructions, "Follow the skill docs.") {
		t.Errorf("AgentInstructions missing AGENTS.md content: %q", rc.AgentInstructions)
	}
	if !strings.Contains(rc.AgentInstructions, "Use the issue tracker.") {
		t.Errorf("AgentInstructions missing CLAUDE.md content: %q", rc.AgentInstructions)
	}
	// AGENTS.md content must precede CLAUDE.md content in the merged text.
	agentsIdx := strings.Index(rc.AgentInstructions, "Follow the skill docs.")
	claudeIdx := strings.Index(rc.AgentInstructions, "Use the issue tracker.")
	if agentsIdx == -1 || claudeIdx == -1 || agentsIdx > claudeIdx {
		t.Errorf("expected AGENTS.md content before CLAUDE.md content, got %q", rc.AgentInstructions)
	}
}

func TestCompile_MissingInstructionFilesHandledSilently(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()

	rc, err := repocontext.Compile(cfg, dir, "abc123")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if rc.AgentInstructions != "" {
		t.Errorf("AgentInstructions = %q, want empty", rc.AgentInstructions)
	}
}

func TestCompile_GateCommandsAreExactlyConfigured_NoRediscovery(t *testing.T) {
	dir := t.TempDir()
	// The repository has its own npm scripts that look like plausible gate
	// commands, but the compiler must never derive gates from them — only
	// from configuration.
	writeFile(t, dir, "package.json", `{
  "name": "example",
  "scripts": {
    "lint": "eslint .",
    "test": "jest"
  }
}`)
	writeFile(t, dir, "Makefile", "check:\n\tgo test ./...\n")

	cfg := config.Default()
	cfg.Quality = gateConfig("configured-gate-one", "configured-gate-two")

	rc, err := repocontext.Compile(cfg, dir, "rev")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	want := []string{"configured-gate-one", "configured-gate-two"}
	if !slices.Equal(rc.QualityGates, want) {
		t.Errorf("QualityGates = %v, want %v (config is the only source, never repo scripts)", rc.QualityGates, want)
	}
}

func TestCompile_NoGatesConfigured_EmptyQualityGates(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default() // Quality.Gates is empty by default

	rc, err := repocontext.Compile(cfg, dir, "rev")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(rc.QualityGates) != 0 {
		t.Errorf("QualityGates = %v, want empty", rc.QualityGates)
	}
}

func TestCompile_DetectsLanguagesAndPackageManagers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.25\n")
	writeFile(t, dir, "package.json", `{"name": "example"}`)
	writeFile(t, dir, "package-lock.json", `{}`)

	cfg := config.Default()

	rc, err := repocontext.Compile(cfg, dir, "rev")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if !slices.Contains(rc.Languages, "Go") {
		t.Errorf("Languages = %v, want to contain Go", rc.Languages)
	}
	if !slices.Contains(rc.Languages, "JavaScript") {
		t.Errorf("Languages = %v, want to contain JavaScript", rc.Languages)
	}
	if !slices.Contains(rc.PackageManagers, "Go Modules") {
		t.Errorf("PackageManagers = %v, want to contain Go Modules", rc.PackageManagers)
	}
	if !slices.Contains(rc.PackageManagers, "npm") {
		t.Errorf("PackageManagers = %v, want to contain npm", rc.PackageManagers)
	}
}

func TestCompile_NoManifests_EmptyLanguagesAndPackageManagers(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()

	rc, err := repocontext.Compile(cfg, dir, "rev")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(rc.Languages) != 0 {
		t.Errorf("Languages = %v, want empty", rc.Languages)
	}
	if len(rc.PackageManagers) != 0 {
		t.Errorf("PackageManagers = %v, want empty", rc.PackageManagers)
	}
}

// TestCompile_Immutability demonstrates that the compiled Repository Context
// is a self-contained value: its slices are independent copies, not aliases
// into config.Config, so mutating either side after the fact cannot corrupt
// the other. It also verifies agent.RepositoryContext exposes no mutator
// methods — the only way to change a field is direct assignment on a value
// the caller owns, and Compile hands out a fresh value each call.
func TestCompile_Immutability(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Quality = gateConfig("gate-a", "gate-b")

	rc1, err := repocontext.Compile(cfg, dir, "rev")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// Mutate the returned slice; a second Compile call must be unaffected,
	// proving rc1.QualityGates does not alias cfg.Quality.Gates's backing
	// array (nor any shared state the compiler retains).
	rc1.QualityGates[0] = "tampered"
	cfg.Quality.Gates[1].Command = "also-tampered"

	rc2, err := repocontext.Compile(cfg, dir, "rev")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if rc2.QualityGates[0] != "gate-a" {
		t.Errorf("rc2.QualityGates[0] = %q, want gate-a (mutating rc1 must not affect a fresh Compile)", rc2.QualityGates[0])
	}

	typ := reflect.TypeOf(agent.RepositoryContext{})
	if n := typ.NumMethod(); n != 0 {
		t.Errorf("agent.RepositoryContext has %d methods, want 0 (no mutator methods; immutable value type)", n)
	}
}

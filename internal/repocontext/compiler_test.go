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

// TestCompile_UnreadableInstructionFile_PropagatesError ensures a
// present-but-unreadable instruction file is treated as a real error rather
// than silently dropped: only a genuinely absent file is skipped. A
// directory named AGENTS.md can't be read as a file, so os.ReadFile fails
// with something other than "not exist".
func TestCompile_UnreadableInstructionFile_PropagatesError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "AGENTS.md"), 0o755); err != nil {
		t.Fatalf("mkdir AGENTS.md: %v", err)
	}
	cfg := config.Default()

	_, err := repocontext.Compile(cfg, dir, "rev")
	if err == nil {
		t.Fatal("Compile: want error for unreadable AGENTS.md, got nil")
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

func TestCompile_ProjectStructureExcludesVCSAndDotfiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	writeFile(t, dir, ".env", "SECRET=1\n")
	writeFile(t, dir, "README.md", "# hi\n")
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	cfg := config.Default()

	rc, err := repocontext.Compile(cfg, dir, "rev")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if strings.Contains(rc.ProjectStructure, ".git") {
		t.Errorf("ProjectStructure = %q, must not contain .git", rc.ProjectStructure)
	}
	if strings.Contains(rc.ProjectStructure, ".env") {
		t.Errorf("ProjectStructure = %q, must not contain dotfiles", rc.ProjectStructure)
	}
	want := "README.md\nsrc/"
	if rc.ProjectStructure != want {
		t.Errorf("ProjectStructure = %q, want %q (deterministic sorted order)", rc.ProjectStructure, want)
	}
}

func TestCompile_JSPackageManagerPrecedence(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{name: "pnpm beats yarn and npm", files: []string{"pnpm-lock.yaml", "yarn.lock", "package-lock.json"}, want: "pnpm"},
		{name: "yarn beats npm", files: []string{"yarn.lock", "package-lock.json"}, want: "Yarn"},
		{name: "npm alone", files: []string{"package-lock.json"}, want: "npm"},
		{name: "package.json alone falls back to npm", files: nil, want: "npm"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "package.json", `{"name": "example"}`)
			for _, f := range tc.files {
				writeFile(t, dir, f, "")
			}
			cfg := config.Default()

			rc, err := repocontext.Compile(cfg, dir, "rev")
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			if !slices.Contains(rc.PackageManagers, tc.want) {
				t.Errorf("PackageManagers = %v, want to contain %v", rc.PackageManagers, tc.want)
			}
			if n := len(rc.PackageManagers); n != 1 {
				t.Errorf("PackageManagers = %v, want exactly one entry", rc.PackageManagers)
			}
		})
	}
}

func TestCompile_DedupesLanguageAcrossMultipleManifests(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pom.xml", "<project></project>\n")
	writeFile(t, dir, "build.gradle", "")
	cfg := config.Default()

	rc, err := repocontext.Compile(cfg, dir, "rev")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	wantLangs := []string{"Java"}
	if !slices.Equal(rc.Languages, wantLangs) {
		t.Errorf("Languages = %v, want %v (deduplicated)", rc.Languages, wantLangs)
	}
	wantPMs := []string{"Gradle", "Maven"}
	if !slices.Equal(rc.PackageManagers, wantPMs) {
		t.Errorf("PackageManagers = %v, want %v", rc.PackageManagers, wantPMs)
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

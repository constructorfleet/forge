package initdiscovery

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/config"
	"gopkg.in/yaml.v3"
)

func runGitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// initRepo creates a bare-bones git repo at dir with a "main" branch, one
// commit, and an "origin" remote pointing at a github.com URL (no network
// access required — remote URLs don't need to be reachable for the plumbing
// this package invokes).
func initRepo(t *testing.T, dir string) {
	t.Helper()
	runGitT(t, dir, "init", "-q", "-b", "main")
	runGitT(t, dir, "config", "user.email", "test@example.com")
	runGitT(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, dir, "add", "README.md")
	runGitT(t, dir, "commit", "-q", "-m", "init")
	runGitT(t, dir, "remote", "add", "origin", "https://github.com/example/repo.git")
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gate(t *testing.T, cfg config.Config, kind string) (string, bool) {
	t.Helper()
	for _, g := range cfg.Quality.Gates {
		if g.Name == kind {
			return g.Command, true
		}
	}
	return "", false
}

func mustLoadable(t *testing.T, result Result) config.Config {
	t.Helper()
	out, err := Render(result)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".forge.yaml")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load(rendered output) failed: %v\n---\n%s", err, out)
	}
	return cfg
}

func TestDetect_GoProject(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.25\n")
	writeFile(t, dir, ".golangci.yml", "run:\n  timeout: 5m\n")

	result, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if cmd, ok := gate(t, result.Config, "build"); !ok || cmd != "go build ./..." {
		t.Errorf("build = %q, %v", cmd, ok)
	}
	if cmd, ok := gate(t, result.Config, "test"); !ok || cmd != "go test ./..." {
		t.Errorf("test = %q, %v", cmd, ok)
	}
	if cmd, ok := gate(t, result.Config, "typecheck"); !ok || cmd != "go vet ./..." {
		t.Errorf("typecheck = %q, %v", cmd, ok)
	}
	if cmd, ok := gate(t, result.Config, "format-check"); !ok || cmd != "gofmt -l ." {
		t.Errorf("format-check = %q, %v", cmd, ok)
	}
	if cmd, ok := gate(t, result.Config, "lint"); !ok || cmd != "golangci-lint run" {
		t.Errorf("lint = %q, %v", cmd, ok)
	}

	loaded := mustLoadable(t, result)
	if len(loaded.Quality.Gates) != 5 {
		t.Errorf("loaded gates = %+v, want 5", loaded.Quality.Gates)
	}
}

func TestDetect_GoProject_NoLintConfig_LeavesUnresolvedMarker(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.25\n")

	result, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if _, ok := gate(t, result.Config, "lint"); ok {
		t.Errorf("lint should be unresolved (no golangci-lint config), got a gate")
	}

	found := false
	for _, n := range result.Notes {
		if n.Field == "quality.gates" && strings.Contains(n.Message, "lint") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a Note about unresolved lint, got %+v", result.Notes)
	}

	out, err := Render(result)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(out), "TODO") {
		t.Errorf("rendered output has no TODO marker:\n%s", out)
	}
	mustLoadable(t, result)
}

func TestDetect_NodePnpmProject(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	writeFile(t, dir, "package.json", `{
  "name": "example",
  "scripts": {
    "test": "vitest run",
    "lint": "eslint .",
    "build": "tsc -p .",
    "typecheck": "tsc --noEmit"
  }
}`)
	writeFile(t, dir, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")

	result, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if cmd, ok := gate(t, result.Config, "test"); !ok || cmd != "pnpm test" {
		t.Errorf("test = %q, %v", cmd, ok)
	}
	if cmd, ok := gate(t, result.Config, "lint"); !ok || cmd != "pnpm run lint" {
		t.Errorf("lint = %q, %v", cmd, ok)
	}
	if cmd, ok := gate(t, result.Config, "build"); !ok || cmd != "pnpm run build" {
		t.Errorf("build = %q, %v", cmd, ok)
	}
	if cmd, ok := gate(t, result.Config, "typecheck"); !ok || cmd != "pnpm run typecheck" {
		t.Errorf("typecheck = %q, %v", cmd, ok)
	}
	if _, ok := gate(t, result.Config, "format-check"); ok {
		t.Errorf("format-check should be unresolved (no matching script)")
	}

	mustLoadable(t, result)
}

func TestDetect_PythonProject(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	writeFile(t, dir, "pyproject.toml", `[project]
name = "example"

[tool.pytest.ini_options]
testpaths = ["tests"]

[tool.ruff]
line-length = 100

[tool.black]
line-length = 100

[build-system]
requires = ["setuptools"]
`)

	result, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if cmd, ok := gate(t, result.Config, "test"); !ok || cmd != "pytest" {
		t.Errorf("test = %q, %v", cmd, ok)
	}
	if cmd, ok := gate(t, result.Config, "lint"); !ok || cmd != "ruff check ." {
		t.Errorf("lint = %q, %v", cmd, ok)
	}
	if cmd, ok := gate(t, result.Config, "format-check"); !ok || cmd != "black --check ." {
		t.Errorf("format-check = %q, %v", cmd, ok)
	}
	if cmd, ok := gate(t, result.Config, "build"); !ok || cmd != "python -m build" {
		t.Errorf("build = %q, %v", cmd, ok)
	}
	if _, ok := gate(t, result.Config, "typecheck"); ok {
		t.Errorf("typecheck should be unresolved (no mypy config)")
	}

	mustLoadable(t, result)
}

func TestDetect_PythonProject_ConventionDefaultTest(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	writeFile(t, dir, "pyproject.toml", "[project]\nname = \"example\"\n")

	result, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if cmd, ok := gate(t, result.Config, "test"); !ok || cmd != "pytest" {
		t.Errorf("test = %q, %v, want convention default pytest", cmd, ok)
	}
	mustLoadable(t, result)
}

func TestDetect_RustProject(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"example\"\nversion = \"0.1.0\"\n")

	result, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if cmd, ok := gate(t, result.Config, "build"); !ok || cmd != "cargo build" {
		t.Errorf("build = %q, %v", cmd, ok)
	}
	if cmd, ok := gate(t, result.Config, "test"); !ok || cmd != "cargo test" {
		t.Errorf("test = %q, %v", cmd, ok)
	}
	if cmd, ok := gate(t, result.Config, "format-check"); !ok || cmd != "cargo fmt -- --check" {
		t.Errorf("format-check = %q, %v", cmd, ok)
	}
	if cmd, ok := gate(t, result.Config, "typecheck"); !ok || cmd != "cargo check" {
		t.Errorf("typecheck = %q, %v", cmd, ok)
	}
	if cmd, ok := gate(t, result.Config, "lint"); !ok || cmd != "cargo clippy -- -D warnings" {
		t.Errorf("lint = %q, %v (convention default)", cmd, ok)
	}

	mustLoadable(t, result)
}

func TestDetect_PriorityExplicitBeatsCIBeatsConvention(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	// Rust gives an explicit test command and a convention-tier lint
	// command (clippy).
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"example\"\nversion = \"0.1.0\"\n")
	// CI hints a different lint command; explicit-from-language-detector
	// doesn't exist for lint in the Rust detector's explicit tier (only
	// convention), so this exercises CI beating convention.
	writeFile(t, dir, ".github/workflows/ci.yml", `on: push
jobs:
  build:
    steps:
      - run: cargo clippy --all-targets -- -D warnings
      - run: cargo test
`)

	result, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	// CI beats the Rust detector's convention-tier clippy command.
	if cmd, ok := gate(t, result.Config, "lint"); !ok || cmd != "cargo clippy --all-targets -- -D warnings" {
		t.Errorf("lint = %q, %v, want CI-derived command to beat convention default", cmd, ok)
	}
	// Explicit (from Cargo.toml detection) beats CI's "cargo test" line.
	if cmd, ok := gate(t, result.Config, "test"); !ok || cmd != "cargo test" {
		t.Errorf("test = %q, %v", cmd, ok)
	}

	mustLoadable(t, result)
}

func TestDetect_Makefile_FillsUnresolvedGate(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.25\n")
	// Go's detector never resolves lint without a golangci-lint config;
	// a Makefile target should fill it.
	writeFile(t, dir, "Makefile", "lint:\n\tgolangci-lint run ./...\n\ntest:\n\tgo test ./...\n")

	result, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if cmd, ok := gate(t, result.Config, "lint"); !ok || cmd != "make lint" {
		t.Errorf("lint = %q, %v", cmd, ok)
	}
	// Go's own explicit "go test ./..." still wins over the Makefile's
	// "test" target since language detectors run first.
	if cmd, ok := gate(t, result.Config, "test"); !ok || cmd != "go test ./..." {
		t.Errorf("test = %q, %v", cmd, ok)
	}
	mustLoadable(t, result)
}

func TestDetect_BaseBranch_FromOriginHEAD(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	runGitT(t, dir, "update-ref", "refs/remotes/origin/develop", "HEAD")
	runGitT(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")

	result, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if result.Config.Git.Base != "origin/develop" {
		t.Errorf("Git.Base = %q, want origin/develop", result.Config.Git.Base)
	}
	for _, n := range result.Notes {
		if n.Field == "git.base" {
			t.Errorf("did not expect a git.base Note when origin/HEAD is set, got %+v", n)
		}
	}
	mustLoadable(t, result)
}

func TestDetect_BaseBranch_FallsBackToLocalMain_WithNote(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	// No refs/remotes/origin/HEAD, no init.defaultBranch set explicitly
	// beyond what `git init -b main` implies locally — local "main"
	// branch exists, so that's the fallback signal.

	result, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if result.Config.Git.Base != "origin/main" {
		t.Errorf("Git.Base = %q, want origin/main", result.Config.Git.Base)
	}
	mustLoadable(t, result)
}

func TestDetect_BaseBranch_Unresolved_LeavesNoteAndValidDefault(t *testing.T) {
	dir := t.TempDir()
	// A repo with no commits and no branches at all: git init without any
	// commit leaves HEAD unborn, so show-ref for main/master fails too.
	runGitT(t, dir, "init", "-q", "-b", "totally-custom")

	result, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if result.Config.Git.Base != config.Default().Git.Base {
		t.Errorf("Git.Base = %q, want default %q when nothing resolves", result.Config.Git.Base, config.Default().Git.Base)
	}
	found := false
	for _, n := range result.Notes {
		if n.Field == "git.base" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a git.base Note when base branch is unresolved, got %+v", result.Notes)
	}
	mustLoadable(t, result)
}

func TestDetect_TrackerType_NonGithubRemote_LeavesNote(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	runGitT(t, dir, "remote", "set-url", "origin", "https://gitlab.com/example/repo.git")

	result, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if result.Config.Tracker.Type != "github" {
		t.Errorf("Tracker.Type = %q, want default github kept in place", result.Config.Tracker.Type)
	}
	found := false
	for _, n := range result.Notes {
		if n.Field == "tracker.type" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a tracker.type Note for a non-github remote, got %+v", result.Notes)
	}
	mustLoadable(t, result)
}

func TestDetect_AgentDocs_Presence(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	writeFile(t, dir, "AGENTS.md", "# Agents\n")

	result, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	found := false
	for _, n := range result.Notes {
		if n.Field == "agent_instructions" && strings.Contains(n.Message, "AGENTS.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an agent_instructions Note mentioning AGENTS.md, got %+v", result.Notes)
	}
}

func TestDetect_AgentDocs_Absence(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	result, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	found := false
	for _, n := range result.Notes {
		if n.Field == "agent_instructions" && strings.Contains(n.Message, "no AGENTS.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an agent_instructions Note noting absence, got %+v", result.Notes)
	}
}

func TestDetect_EmptyRepo_StillProducesValidLoadableConfig(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	result, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	loaded := mustLoadable(t, result)
	// Compare via their YAML encoding rather than reflect.DeepEqual: yaml.v3
	// decodes an empty YAML sequence/mapping ("[]"/"{}") into a non-nil
	// empty slice/map, so a nil field in result.Config and its round-tripped
	// non-nil-but-empty counterpart are semantically identical even though
	// DeepEqual would (correctly, but unhelpfully here) call them different.
	wantYAML, err := yaml.Marshal(result.Config)
	if err != nil {
		t.Fatalf("yaml.Marshal(result.Config): %v", err)
	}
	gotYAML, err := yaml.Marshal(loaded)
	if err != nil {
		t.Fatalf("yaml.Marshal(loaded): %v", err)
	}
	if string(gotYAML) != string(wantYAML) {
		t.Errorf("round-tripped config differs from detected config:\ngot:\n%s\nwant:\n%s", gotYAML, wantYAML)
	}
}

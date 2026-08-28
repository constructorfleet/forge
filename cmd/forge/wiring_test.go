package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/agent/claude"
	"github.com/Teagan42/forge/internal/config"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func newTempRepo(t *testing.T) (root, base string) {
	t.Helper()
	root = t.TempDir()
	runGit(t, root, "init", "-q", "-b", "main")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-q", "-m", "initial")
	sha := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	return root, sha
}

func TestLoadConfig_FallsBackToDefaultWhenAbsent(t *testing.T) {
	cfg, err := loadConfig(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if !reflect.DeepEqual(cfg, config.Default()) {
		t.Errorf("loadConfig with no file = %+v, want config.Default()", cfg)
	}
}

func TestLoadConfig_LoadsPresentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".forge.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nagent:\n  provider: fake\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Agent.Provider != "fake" {
		t.Errorf("Agent.Provider = %q, want fake", cfg.Agent.Provider)
	}
}

func TestLoadConfig_PropagatesParseErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".forge.yaml")
	if err := os.WriteFile(path, []byte("version: [not, a, scalar]\n  bad indent:x\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("loadConfig: want error for malformed YAML, got nil")
	}
}

func TestOpenStore_CreatesParentDirAndMigrates(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "nested", "forge.db")
	store, err := openStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("db file not created at %s: %v", dbPath, err)
	}
}

func TestResolveBaseRevision_ResolvesRefToSHA(t *testing.T) {
	root, base := newTempRepo(t)

	got, err := resolveBaseRevision(root, "HEAD")
	if err != nil {
		t.Fatalf("resolveBaseRevision: %v", err)
	}
	if got != base {
		t.Errorf("resolveBaseRevision(HEAD) = %s, want %s", got, base)
	}
}

func TestResolveBaseRevision_ErrorsOnUnknownRef(t *testing.T) {
	root, _ := newTempRepo(t)
	if _, err := resolveBaseRevision(root, "origin/does-not-exist"); err == nil {
		t.Fatal("resolveBaseRevision: want error for unresolvable ref, got nil")
	}
}

func TestRepoFromOrigin_ParsesSSHAndHTTPSRemotes(t *testing.T) {
	cases := []struct {
		name      string
		remoteURL string
		wantOwner string
		wantRepo  string
	}{
		{"ssh", "git@github.com:acme/widgets.git", "acme", "widgets"},
		{"https_with_git_suffix", "https://github.com/acme/widgets.git", "acme", "widgets"},
		{"https_no_suffix", "https://github.com/acme/widgets", "acme", "widgets"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := newTempRepo(t)
			runGit(t, root, "remote", "add", "origin", tc.remoteURL)

			owner, repo, err := repoFromOrigin(root)
			if err != nil {
				t.Fatalf("repoFromOrigin: %v", err)
			}
			if owner != tc.wantOwner || repo != tc.wantRepo {
				t.Errorf("repoFromOrigin() = (%s, %s), want (%s, %s)", owner, repo, tc.wantOwner, tc.wantRepo)
			}
		})
	}
}

func TestRepoFromOrigin_ErrorsWithoutOriginRemote(t *testing.T) {
	root, _ := newTempRepo(t)
	if _, _, err := repoFromOrigin(root); err == nil {
		t.Fatal("repoFromOrigin: want error with no 'origin' remote, got nil")
	}
}

func TestRepoFromOrigin_ErrorsOnUnrecognizedRemote(t *testing.T) {
	root, _ := newTempRepo(t)
	runGit(t, root, "remote", "add", "origin", "https://gitlab.com/acme/widgets.git")
	if _, _, err := repoFromOrigin(root); err == nil {
		t.Fatal("repoFromOrigin: want error for a non-GitHub remote, got nil")
	}
}

func TestBuildAgent_SelectsByProvider(t *testing.T) {
	cases := []struct {
		provider string
		wantFake bool
		wantErr  bool
	}{
		{"fake", true, false},
		{"claude-code", false, false},
		{"", false, false},
		{"unknown-provider", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			cfg := config.Default()
			cfg.Agent.Provider = tc.provider
			ag, err := buildAgent(cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatal("buildAgent: want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("buildAgent: %v", err)
			}
			if tc.wantFake {
				if _, ok := ag.(*agent.FakeAgent); !ok {
					t.Errorf("buildAgent(%q) = %T, want *agent.FakeAgent", tc.provider, ag)
				}
			} else if _, ok := ag.(*claude.Adapter); !ok {
				t.Errorf("buildAgent(%q) = %T, want *claude.Adapter", tc.provider, ag)
			}
		})
	}
}

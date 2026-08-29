package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/agent/claude"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/tracker/github"
)

// runGit and newTempRepo delegate to internal/gittest, the shared fixture
// used by internal/engine and internal/workspace's tests too.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gittest.RunGit(t, dir, args...)
}

func newTempRepo(t *testing.T) (root, base string) {
	t.Helper()
	return gittest.NewTempRepo(t)
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
			} else if adapter, ok := ag.(*claude.Adapter); !ok {
				t.Errorf("buildAgent(%q) = %T, want *claude.Adapter", tc.provider, ag)
			} else if adapter.PermissionMode != string(cfg.Agent.PermissionMode) {
				t.Errorf("buildAgent(%q).PermissionMode = %q, want %q", tc.provider, adapter.PermissionMode, cfg.Agent.PermissionMode)
			}
		})
	}
}

func TestVerifyTrackerAuth_MissingTokenErrorsBeforeAnyRequest(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")

	root, _ := newTempRepo(t)
	runGit(t, root, "remote", "add", "origin", "git@github.com:acme/widgets.git")

	cfg := config.Default()
	dbPath := filepath.Join(t.TempDir(), "forge.db")

	if err := verifyTrackerAuth(context.Background(), cfg, root); !errors.Is(err, github.ErrMissingToken) {
		t.Fatalf("verifyTrackerAuth: want github.ErrMissingToken, got %v", err)
	}

	// No side effects: verifyTrackerAuth runs before openStore, so nothing
	// should have created the state database.
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Errorf("expected no state database to be created, stat error = %v", err)
	}
}

func TestVerifyTrackerAuth_NoOriginRemoteErrors(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "a-token")

	root, _ := newTempRepo(t)
	cfg := config.Default()

	if err := verifyTrackerAuth(context.Background(), cfg, root); err == nil {
		t.Fatal("verifyTrackerAuth: want error without an 'origin' remote, got nil")
	}
}

func TestVerifyTrackerAuth_SkipAuthPreflightIsAnEscapeHatch(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")

	root, _ := newTempRepo(t)
	// Deliberately no 'origin' remote either: SkipAuthPreflight must skip
	// buildTracker entirely, not just the network call, so this context
	// has zero dependency on a resolvable tracker.
	cfg := config.Default()
	cfg.Tracker.SkipAuthPreflight = true

	if err := verifyTrackerAuth(context.Background(), cfg, root); err != nil {
		t.Fatalf("verifyTrackerAuth with SkipAuthPreflight: want nil, got %v", err)
	}
}

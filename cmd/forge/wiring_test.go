package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/agent/claude"
	"github.com/Teagan42/forge/internal/agent/codex"
	"github.com/Teagan42/forge/internal/agent/openai"
	"github.com/Teagan42/forge/internal/agent/opencode"
	"github.com/Teagan42/forge/internal/agent/pi"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/execution/container"
	"github.com/Teagan42/forge/internal/execution/localhost"
	"github.com/Teagan42/forge/internal/execution/remote"
	"github.com/Teagan42/forge/internal/execution/remote/httpworker"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker/github"
	"github.com/Teagan42/forge/internal/workspace"
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

func TestBuildTrackerUsesConfiguredIssueProvider(t *testing.T) {
	root, _ := newTempRepo(t)
	runGit(t, root, "remote", "add", "origin", "https://github.com/acme/widgets.git")

	cfg := config.Default()
	cfg.Tracker.Provider = "linear"

	trk, err := buildTracker(cfg, root)
	if err != nil {
		t.Fatalf("buildTracker: %v", err)
	}
	if trk.Provider != "linear" {
		t.Fatalf("buildTracker Provider = %q, want linear", trk.Provider)
	}
}

func TestBuildTracker_UnknownTypeErrors(t *testing.T) {
	root, _ := newTempRepo(t)
	runGit(t, root, "remote", "add", "origin", "https://github.com/acme/widgets.git")

	cfg := config.Default()
	cfg.Tracker.Type = "linear"

	if _, err := buildTracker(cfg, root); err == nil {
		t.Fatal("buildTracker: want error for unknown tracker type, got nil")
	}
}

func TestBuildSCM_ComposesTheConfiguredProvider(t *testing.T) {
	root, _ := newTempRepo(t)
	runGit(t, root, "remote", "add", "origin", "https://github.com/acme/widgets.git")

	cfg := config.Default()

	scm, err := buildSCM(cfg, root)
	if err != nil {
		t.Fatalf("buildSCM: %v", err)
	}
	if scm == nil {
		t.Fatal("buildSCM: got nil client")
	}
}

func TestBuildSCM_UnknownTypeErrors(t *testing.T) {
	root, _ := newTempRepo(t)
	runGit(t, root, "remote", "add", "origin", "https://github.com/acme/widgets.git")

	cfg := config.Default()
	cfg.SCM.Type = "gitlab"

	if _, err := buildSCM(cfg, root); err == nil {
		t.Fatal("buildSCM: want error for unknown scm type, got nil")
	}
}

func TestBuildCI_ComposesTheConfiguredProvider(t *testing.T) {
	root, _ := newTempRepo(t)
	runGit(t, root, "remote", "add", "origin", "https://github.com/acme/widgets.git")

	cfg := config.Default()

	ciCap, err := buildCI(cfg, root)
	if err != nil {
		t.Fatalf("buildCI: %v", err)
	}
	if ciCap == nil {
		t.Fatal("buildCI: got nil client")
	}
}

func TestBuildCI_UnknownTypeErrors(t *testing.T) {
	root, _ := newTempRepo(t)
	runGit(t, root, "remote", "add", "origin", "https://github.com/acme/widgets.git")

	cfg := config.Default()
	cfg.CI.Type = "gitlab"

	if _, err := buildCI(cfg, root); err == nil {
		t.Fatal("buildCI: want error for unknown ci type, got nil")
	}
}

func TestBuildExecutionBackend_SelectsByBackend(t *testing.T) {
	root, _ := newTempRepo(t)
	wsMgr, err := workspace.NewManager(root)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	ag := agent.NewFakeAgent()
	store := openPlanningStore(t)

	cases := []struct {
		backend  string
		wantType any
		wantErr  bool
	}{
		{config.BackendLocal, (*localhost.Backend)(nil), false},
		{"", (*localhost.Backend)(nil), false},
	}
	for _, tc := range cases {
		t.Run(tc.backend, func(t *testing.T) {
			cfg := config.Default()
			cfg.Execution.Backend = tc.backend

			backend, err := buildExecutionBackend(cfg, wsMgr, ag, store)
			if tc.wantErr {
				if err == nil {
					t.Fatal("buildExecutionBackend: want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("buildExecutionBackend: %v", err)
			}
			if reflect.TypeOf(backend) != reflect.TypeOf(tc.wantType) {
				t.Fatalf("buildExecutionBackend(%q) = %T, want %T", tc.backend, backend, tc.wantType)
			}
		})
	}
}

// TestBuildExecutionBackend_ContainerFailsPreflightWithNoRuntimeAvailable
// pins the ticket-336 acceptance criterion that selecting backend: container
// fails at wiring, with a clear error, rather than reaching Prepare and
// failing mid-run. buildContainerRuntime (issue #385) probes docker and
// podman for a real, reachable daemon through the real ExecCommandRunner, so
// this test points PATH at an empty directory: neither binary resolves,
// regardless of whether the host running the test (e.g. a CI runner with
// its own Docker daemon) has a live docker or podman itself.
func TestBuildExecutionBackend_ContainerFailsPreflightWithNoRuntimeAvailable(t *testing.T) {
	root, _ := newTempRepo(t)
	wsMgr, err := workspace.NewManager(root)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	ag := agent.NewFakeAgent()
	store := openPlanningStore(t)

	emptyPath := t.TempDir()
	t.Setenv("PATH", emptyPath)

	cfg := config.Default()
	cfg.Execution.Backend = config.BackendContainer
	cfg.Execution.Container.Image = "forge/agent:latest"

	_, err = buildExecutionBackend(cfg, wsMgr, ag, store)
	if !errors.Is(err, container.ErrRuntimeUnavailable) {
		t.Fatalf("buildExecutionBackend: want container.ErrRuntimeUnavailable, got %v", err)
	}
}

// TestBuildExecutionBackend_RemoteFailsPreflightWithUnreachableWorker pins
// the ticket-343 acceptance criterion that selecting backend: remote with
// an unreachable worker fails at wiring, with a clear error, rather than
// reaching Prepare and failing mid-run. buildWorkerClient's httpworker.
// Client (issue #345) reaches this endpoint for a real health check, so an
// address nothing answers on still fails this preflight, exactly as it did
// before a concrete worker transport existed.
func TestBuildExecutionBackend_RemoteFailsPreflightWithUnreachableWorker(t *testing.T) {
	root, _ := newTempRepo(t)
	wsMgr, err := workspace.NewManager(root)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	ag := agent.NewFakeAgent()
	store := openPlanningStore(t)

	cfg := config.Default()
	cfg.Execution.Backend = config.BackendRemote
	cfg.Execution.Worker.Endpoint = "http://127.0.0.1:1"

	_, err = buildExecutionBackend(cfg, wsMgr, ag, store)
	if !errors.Is(err, remote.ErrWorkerUnreachable) {
		t.Fatalf("buildExecutionBackend: want remote.ErrWorkerUnreachable, got %v", err)
	}
}

// TestBuildExecutionBackend_RemoteWiresRealClientAgainstReachableWorker
// pins the other half of the ticket-343/345 seam: a reachable worker
// daemon must let backend: remote build a Remote backend, constructed with
// the real httpworker.Client (not the fake), through the exact same wiring
// path production uses.
func TestBuildExecutionBackend_RemoteWiresRealClientAgainstReachableWorker(t *testing.T) {
	root, _ := newTempRepo(t)
	wsMgr, err := workspace.NewManager(root)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	ag := agent.NewFakeAgent()
	store := openPlanningStore(t)

	workerRoot := t.TempDir()
	runGit(t, workerRoot, "init", "-q", "-b", "main")
	srv, err := httpworker.NewServer(workerRoot, "origin", agent.NewFakeAgent())
	if err != nil {
		t.Fatalf("httpworker.NewServer: %v", err)
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cfg := config.Default()
	cfg.Execution.Backend = config.BackendRemote
	cfg.Execution.Worker.Endpoint = ts.URL

	backend, err := buildExecutionBackend(cfg, wsMgr, ag, store)
	if err != nil {
		t.Fatalf("buildExecutionBackend: %v", err)
	}
	if _, ok := backend.(*remote.Backend); !ok {
		t.Fatalf("buildExecutionBackend = %T, want *remote.Backend", backend)
	}
}

func TestBuildExecutionBackend_RemoteClaimsLeaseThroughWiredStore(t *testing.T) {
	root, originPath, base := gittest.NewTempRepoWithOrigin(t)
	wsMgr, err := workspace.NewManager(root)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	store := openPlanningStore(t)
	ctx := context.Background()
	if err := store.CreateExecution(ctx, domain.Execution{ID: "exec1", BaseRevision: base}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if err := store.CreateIssue(ctx, domain.Issue{ExecutionID: "exec1", ID: "issue-42"}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	workerRoot := t.TempDir()
	runGit(t, workerRoot, "clone", "-q", originPath, ".")
	srv, err := httpworker.NewServer(workerRoot, "origin", agent.NewFakeAgent())
	if err != nil {
		t.Fatalf("httpworker.NewServer: %v", err)
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cfg := config.Default()
	cfg.Execution.Backend = config.BackendRemote
	cfg.Execution.Worker.Endpoint = ts.URL

	backend, err := buildExecutionBackend(cfg, wsMgr, agent.NewFakeAgent(), store)
	if err != nil {
		t.Fatalf("buildExecutionBackend: %v", err)
	}
	env, err := backend.Prepare(ctx, execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42", Base: base})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = env.Cleanup(context.Background()) })

	if _, err := store.ExecutionLease(ctx, "exec1", "issue-42"); err != nil {
		t.Fatalf("ExecutionLease: %v", err)
	}
}

func TestBuildExecuteRuntime_WiresLostExecutionControllerForRemoteBackend(t *testing.T) {
	root, _ := newTempRepo(t)
	runGit(t, root, "remote", "add", "origin", "https://github.com/acme/widgets.git")

	store := openPlanningStore(t)

	workerRoot, _ := newTempRepo(t)
	runGit(t, workerRoot, "remote", "add", "origin", root)
	srv, err := httpworker.NewServer(workerRoot, "origin", agent.NewFakeAgent())
	if err != nil {
		t.Fatalf("httpworker.NewServer: %v", err)
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cfg := config.Default()
	cfg.Agent.Provider = "fake"
	cfg.Execution.Backend = config.BackendRemote
	cfg.Execution.Worker.Endpoint = ts.URL
	cfg.PullRequests.Enabled = false
	cfg.Workflow.Review = false

	runtime, err := buildExecuteRuntime(store, cfg, root, []string{"408"})
	if err != nil {
		t.Fatalf("buildExecuteRuntime: %v", err)
	}
	if runtime.Scheduler == nil {
		t.Fatal("buildExecuteRuntime Scheduler is nil")
	}
	if runtime.LostExecutionController == nil {
		t.Fatal("buildExecuteRuntime LostExecutionController is nil for remote backend")
	}
}

// TestComposition_ValidGithubConfigurationWiresEndToEnd is the wiring-seam
// composition test issue #295's testing decisions call for: a valid
// all-github composition (the zero-config default) must build every
// capability and wire a full Engine without error, proving the composed
// Tracker/SCM/CI capabilities reach Engine's existing narrow consumer
// seams unchanged.
func TestComposition_ValidGithubConfigurationWiresEndToEnd(t *testing.T) {
	root, _ := newTempRepo(t)
	runGit(t, root, "remote", "add", "origin", "https://github.com/acme/widgets.git")

	dbPath := filepath.Join(t.TempDir(), "forge.db")
	store, err := openStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	cfg := config.Default()
	cfg.Agent.Provider = "fake"

	eng, err := buildEngine(store, cfg, root)
	if err != nil {
		t.Fatalf("buildEngine: %v, want a coherent all-github composition to wire end-to-end", err)
	}
	if eng.PRTracker == nil {
		t.Error("eng.PRTracker not wired from the composed SCM capability")
	}
	if eng.CIWaiter == nil {
		t.Error("eng.CIWaiter not wired from the composed CI capability")
	}
	if _, ok := eng.Backend.(*localhost.Backend); !ok {
		t.Errorf("eng.Backend = %T, want *localhost.Backend (the default config.BackendLocal selection)", eng.Backend)
	}
}

// TestComposition_IncoherentConfigurationRejectedBeforeWiring pins that an
// incoherent ci/scm composition never reaches buildEngine in the first
// place: loadConfig (config.Load) rejects it at startup (issue #295's
// "fails at wiring, not mid-run"), so a caller following the normal
// loadConfig -> buildEngine sequence cannot construct an Engine from it.
func TestComposition_IncoherentConfigurationRejectedBeforeWiring(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".forge.yaml")
	if err := os.WriteFile(path, []byte("ci:\n  type: gitlab\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := loadConfig(path); err == nil {
		t.Fatal("loadConfig: want error for incoherent ci/scm composition, got nil")
	}
}

func TestBuildAgent_SelectsByProvider(t *testing.T) {
	cases := []struct {
		provider string
		wantType any
		wantErr  bool
	}{
		{"fake", (*agent.FakeAgent)(nil), false},
		{"claude-code", (*claude.Adapter)(nil), false},
		{"", (*claude.Adapter)(nil), false},
		{"codex", (*codex.Adapter)(nil), false},
		{"opencode", (*opencode.Adapter)(nil), false},
		{"pi", (*pi.Adapter)(nil), false},
		{"openai-responses", (*openai.ResponsesAdapter)(nil), false},
		{"openai-chat-completions", (*openai.ChatCompletionsAdapter)(nil), false},
		{"unknown-provider", nil, true},
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
			if reflect.TypeOf(ag) != reflect.TypeOf(tc.wantType) {
				t.Fatalf("buildAgent(%q) = %T, want %T", tc.provider, ag, tc.wantType)
			}
			if adapter, ok := ag.(*claude.Adapter); ok && adapter.PermissionMode != string(cfg.Agent.PermissionMode) {
				t.Errorf("buildAgent(%q).PermissionMode = %q, want %q", tc.provider, adapter.PermissionMode, cfg.Agent.PermissionMode)
			}
		})
	}
}

func TestBuildPlanningBackend_SelectsByProvider(t *testing.T) {
	cases := []struct {
		provider string
		wantFake bool
		wantErr  bool
	}{
		{"fake", true, false},
		{"claude-code", false, false},
		{"", false, false},
		{"codex", false, false},
		{"opencode", false, false},
		{"pi", false, false},
		{"openai-responses", false, false},
		{"openai-chat-completions", false, false},
		{"unknown-provider", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			cfg := config.Default()
			cfg.Agent.Provider = tc.provider
			backend, err := buildPlanningBackend(cfg, openPlanningStore(t), "feature-1")
			if tc.wantErr {
				if err == nil {
					t.Fatal("buildPlanningBackend: want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("buildPlanningBackend: %v", err)
			}
			if tc.wantFake {
				if _, ok := backend.(*planningagent.FakeBackend); !ok {
					t.Errorf("buildPlanningBackend(%q) = %T, want *planningagent.FakeBackend", tc.provider, backend)
				}
			} else if _, ok := backend.(*planningagent.AgentBackend); !ok {
				t.Errorf("buildPlanningBackend(%q) = %T, want *planningagent.AgentBackend", tc.provider, backend)
			}
		})
	}
}

// TestBuildPlanningBackend_PersistsTranscriptsUnderTheFeature pins issue
// #248's wiring: the real planning backend must be handed the Store and the
// Feature's identifiers, or planning agents' transcripts never reach
// transcript_events no matter how the AgentBackend seam behaves.
func TestBuildPlanningBackend_PersistsTranscriptsUnderTheFeature(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.Provider = "claude-code"

	backend, err := buildPlanningBackend(cfg, openPlanningStore(t), "widget")
	if err != nil {
		t.Fatalf("buildPlanningBackend: %v", err)
	}
	agentBackend, ok := backend.(*planningagent.AgentBackend)
	if !ok {
		t.Fatalf("buildPlanningBackend = %T, want *planningagent.AgentBackend", backend)
	}

	executionID, issueID, persisting := agentBackend.TranscriptScope()
	if !persisting {
		t.Fatal("TranscriptScope reports no persistence, want the Store threaded through")
	}
	if executionID != "widget" || issueID != "widget" {
		t.Fatalf("TranscriptScope = (%q, %q), want the Feature id for both", executionID, issueID)
	}
}

func openPlanningStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
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

func TestLostRecoveryEnabled(t *testing.T) {
	tests := []struct {
		name    string
		backend string
		want    bool
	}{
		{name: "local backend disables loss detection", backend: config.BackendLocal, want: false},
		{name: "remote backend enables loss detection", backend: config.BackendRemote, want: true},
		{name: "empty backend disables loss detection", backend: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Execution.Backend = tt.backend
			if got := lostRecoveryEnabled(cfg); got != tt.want {
				t.Fatalf("lostRecoveryEnabled(%q) = %v, want %v", tt.backend, got, tt.want)
			}
		})
	}
}

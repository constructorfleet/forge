package lspdriver

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGoplsPath is built once in TestMain and reused by every test that
// needs a stdio LSP server subprocess, avoiding a `go build` per test.
var fakeGoplsPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fakegopls-bin")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	fakeGoplsPath = filepath.Join(dir, "fakegopls")
	build := exec.Command("go", "build", "-o", fakeGoplsPath, "./testdata/fakegopls")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		panic("build fakegopls fixture: " + err.Error() + "\n" + string(out))
	}

	os.Exit(m.Run())
}

// spawnLog returns a fresh temp file path for FAKEGOPLS_SPAWN_LOG and a
// reader function that counts how many "spawn" lines it recorded, so a test
// can assert exactly how many times the fixture process was launched.
func spawnLog(t *testing.T) (path string, count func() int) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "spawns")
	if err != nil {
		t.Fatalf("create spawn log: %v", err)
	}
	path = f.Name()
	_ = f.Close()

	return path, func() int {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read spawn log: %v", err)
		}
		n := 0
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			n++
		}
		return n
	}
}

func TestDriver_StartCompletesHandshakeAndExposesCapabilities(t *testing.T) {
	dir := t.TempDir()
	d := New(Options{
		Command:          []string{fakeGoplsPath},
		Dir:              dir,
		ReadinessTimeout: 5 * time.Second,
		RestartLimit:     1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d.Start(ctx)
	defer func() { _ = d.Shutdown(context.Background()) }()

	caps := d.Capabilities()
	if caps.HoverProvider == nil {
		t.Fatalf("Capabilities().HoverProvider = %#v, want the fixture's advertised hover support", caps.HoverProvider)
	}
}

func TestDriver_ReadinessTimeoutDegradesToInertWithoutError(t *testing.T) {
	dir := t.TempDir()
	d := New(Options{
		Command:          []string{fakeGoplsPath},
		Dir:              dir,
		Env:              []string{"FAKEGOPLS_MODE=hang"},
		ReadinessTimeout: 200 * time.Millisecond,
		RestartLimit:     0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d.Start(ctx) // must not panic or block past the readiness timeout

	caps := d.Capabilities()
	if caps.HoverProvider != nil {
		t.Errorf("Capabilities().HoverProvider = %#v, want nil (inert) after readiness timeout", caps.HoverProvider)
	}
}

func TestDriver_CrashRestartsOnceThenGoesInert(t *testing.T) {
	dir := t.TempDir()
	logPath, spawns := spawnLog(t)
	d := New(Options{
		Command: []string{fakeGoplsPath},
		Dir:     dir,
		Env: []string{
			"FAKEGOPLS_MODE=crash-after-init",
			"FAKEGOPLS_SPAWN_LOG=" + logPath,
		},
		ReadinessTimeout: 2 * time.Second,
		RestartLimit:     1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d.Start(ctx)

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if spawns() >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Give the driver a moment to process the second crash and settle.
	time.Sleep(300 * time.Millisecond)

	if got := spawns(); got != 2 {
		t.Fatalf("spawn count = %d, want exactly 2 (initial start + one restart, RestartLimit=1)", got)
	}
	if caps := d.Capabilities(); caps.HoverProvider != nil {
		t.Errorf("Capabilities().HoverProvider = %#v, want nil (inert) after exhausting RestartLimit", caps.HoverProvider)
	}
}

func TestDriver_HandshakeSendsProfileInitOptions(t *testing.T) {
	dir := t.TempDir()
	f, err := os.CreateTemp(t.TempDir(), "init-options")
	if err != nil {
		t.Fatalf("create init-options log: %v", err)
	}
	initLogPath := f.Name()
	_ = f.Close()

	d := New(Options{
		Command:          []string{fakeGoplsPath},
		Dir:              dir,
		Env:              []string{"FAKEGOPLS_INIT_OPTIONS_LOG=" + initLogPath},
		ReadinessTimeout: 5 * time.Second,
		RestartLimit:     0,
		Profile: ServerProfile{
			InitOptions: map[string]any{"tsserver": map[string]any{"path": "/usr/lib/node_modules/typescript/lib"}},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d.Start(ctx)
	defer func() { _ = d.Shutdown(context.Background()) }()

	if caps := d.Capabilities(); caps.HoverProvider == nil {
		t.Fatal("driver did not become ready")
	}

	data, err := os.ReadFile(initLogPath)
	if err != nil {
		t.Fatalf("read init-options log: %v", err)
	}
	got := strings.TrimSpace(string(data))
	if !strings.Contains(got, `"tsserver"`) || !strings.Contains(got, `"path"`) {
		t.Fatalf("initializationOptions received = %q, want it to contain the profile's InitOptions", got)
	}
}

func TestDriver_HandshakeSendsNoInitOptionsForZeroProfile(t *testing.T) {
	dir := t.TempDir()
	f, err := os.CreateTemp(t.TempDir(), "init-options")
	if err != nil {
		t.Fatalf("create init-options log: %v", err)
	}
	initLogPath := f.Name()
	_ = f.Close()

	d := New(Options{
		Command:          []string{fakeGoplsPath},
		Dir:              dir,
		Env:              []string{"FAKEGOPLS_INIT_OPTIONS_LOG=" + initLogPath},
		ReadinessTimeout: 5 * time.Second,
		RestartLimit:     0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d.Start(ctx)
	defer func() { _ = d.Shutdown(context.Background()) }()

	if caps := d.Capabilities(); caps.HoverProvider == nil {
		t.Fatal("driver did not become ready")
	}

	data, err := os.ReadFile(initLogPath)
	if err != nil {
		t.Fatalf("read init-options log: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "null" {
		t.Fatalf("initializationOptions received = %q, want %q for the zero ServerProfile", got, "null")
	}
}

func TestDriver_ShutdownTerminatesSubprocessCleanly(t *testing.T) {
	dir := t.TempDir()
	d := New(Options{
		Command:          []string{fakeGoplsPath},
		Dir:              dir,
		ReadinessTimeout: 5 * time.Second,
		RestartLimit:     1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d.Start(ctx)

	if caps := d.Capabilities(); caps.HoverProvider == nil {
		t.Fatal("driver did not become ready before Shutdown")
	}

	if err := d.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

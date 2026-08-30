package lspdriver

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureFile is testdata/fixture/greet.go, whose exact line layout
// fakegopls's fakeServer hardcodes responses against (see that fixture's
// doc comment). Positions below are this package's 1-based Position, one
// past fakegopls's 0-based line/character constants.
const fixtureFile = "testdata/fixture/greet.go"

// greetDefPos is Greet's own definition: "func Greet(name string) string {"
// on line 16, where "Greet" starts at column 6.
var greetDefPos = Position{Line: 16, Column: 6}

// greetCallPos is the Greet("world") call inside Caller, on line 22, where
// "Greet" starts at column 9.
var greetCallPos = Position{Line: 22, Column: 9}

// callerDefPos is Caller's own definition on line 21, where "Caller"
// starts at column 6.
var callerDefPos = Position{Line: 21, Column: 6}

// greeterDefPos is the Greeter interface's definition on line 8, where
// "Greeter" starts at column 6.
var greeterDefPos = Position{Line: 8, Column: 6}

// englishGreeterDefPos is EnglishGreeter's definition on line 13, where
// "EnglishGreeter" starts at column 6.
var englishGreeterDefPos = Position{Line: 13, Column: 6}

func startTestDriver(t *testing.T, env []string) *Driver {
	t.Helper()
	return startTestDriverWithProfile(t, env, ServerProfile{})
}

// startTestDriverWithProfile is startTestDriver with an explicit
// ServerProfile, for the per-server behaviors (hover shape, symbol-children
// handling) a driver's profile — not its protocol handling — decides.
func startTestDriverWithProfile(t *testing.T, env []string, profile ServerProfile) *Driver {
	t.Helper()
	d := New(Options{
		Command:          []string{fakeGoplsPath},
		Dir:              t.TempDir(),
		Env:              env,
		ReadinessTimeout: 5 * time.Second,
		RestartLimit:     0,
		Profile:          profile,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d.Start(ctx)
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	if d.Capabilities().HoverProvider == nil {
		t.Fatal("driver did not become ready")
	}
	return d
}

func openLog(t *testing.T) (path string, lines func() []string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "opens")
	if err != nil {
		t.Fatalf("create open log: %v", err)
	}
	path = f.Name()
	_ = f.Close()

	return path, func() []string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read open log: %v", err)
		}
		var out []string
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			out = append(out, sc.Text())
		}
		return out
	}
}

func TestDriver_FindDefinition(t *testing.T) {
	d := startTestDriver(t, nil)

	got, err := d.FindDefinition(context.Background(), fixtureFile, greetCallPos)
	if err != nil {
		t.Fatalf("FindDefinition() error = %v", err)
	}

	abs, _ := filepath.Abs(fixtureFile)
	want := []Location{{File: abs, Position: greetDefPos}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("FindDefinition() = %#v, want %#v", got, want)
	}
}

func TestDriver_FindReferences(t *testing.T) {
	d := startTestDriver(t, nil)

	got, err := d.FindReferences(context.Background(), fixtureFile, greetDefPos)
	if err != nil {
		t.Fatalf("FindReferences() error = %v", err)
	}

	abs, _ := filepath.Abs(fixtureFile)
	want := []Location{
		{File: abs, Position: greetDefPos},
		{File: abs, Position: greetCallPos},
	}
	if len(got) != len(want) {
		t.Fatalf("FindReferences() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("FindReferences()[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestDriver_FindImplementations(t *testing.T) {
	d := startTestDriver(t, nil)

	got, err := d.FindImplementations(context.Background(), fixtureFile, greeterDefPos)
	if err != nil {
		t.Fatalf("FindImplementations() error = %v", err)
	}

	abs, _ := filepath.Abs(fixtureFile)
	want := []Location{{File: abs, Position: englishGreeterDefPos}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("FindImplementations() = %#v, want %#v", got, want)
	}
}

func TestDriver_SymbolInfo(t *testing.T) {
	d := startTestDriver(t, nil)

	got, err := d.SymbolInfo(context.Background(), fixtureFile, greetDefPos)
	if err != nil {
		t.Fatalf("SymbolInfo() error = %v", err)
	}

	if got.Signature != "func Greet(name string) string" {
		t.Errorf("SymbolInfo().Signature = %q, want %q", got.Signature, "func Greet(name string) string")
	}
	if !strings.Contains(got.Documentation, "Greet returns an English greeting") {
		t.Errorf("SymbolInfo().Documentation = %q, want it to contain the doc comment", got.Documentation)
	}
	abs, _ := filepath.Abs(fixtureFile)
	if got.Definition == nil || *got.Definition != (Location{File: abs, Position: greetDefPos}) {
		t.Errorf("SymbolInfo().Definition = %#v, want &Location{%s, %#v}", got.Definition, abs, greetDefPos)
	}
}

func TestDriver_DocumentSymbols(t *testing.T) {
	d := startTestDriver(t, nil)

	got, err := d.DocumentSymbols(context.Background(), fixtureFile)
	if err != nil {
		t.Fatalf("DocumentSymbols() error = %v", err)
	}

	names := make([]string, len(got))
	for i, s := range got {
		names[i] = s.Name
	}
	// "name" is Greet's parameter child: with the zero ServerProfile the
	// hierarchical result is flattened whole, children included. Contrast
	// TestDriver_DocumentSymbolsDropsChildrenWhenProfileSet.
	wantNames := []string{"Greeter", "EnglishGreeter", "Greet", "name", "Caller"}
	if len(names) != len(wantNames) {
		t.Fatalf("DocumentSymbols() names = %v, want %v", names, wantNames)
	}
	for i, want := range wantNames {
		if names[i] != want {
			t.Errorf("DocumentSymbols()[%d].Name = %q, want %q", i, names[i], want)
		}
	}

	var greet Symbol
	for _, s := range got {
		if s.Name == "Greet" {
			greet = s
		}
	}
	if greet.Kind != "function" {
		t.Errorf("Greet symbol Kind = %q, want %q", greet.Kind, "function")
	}
	if greet.Location.Position != greetDefPos {
		t.Errorf("Greet symbol Position = %#v, want %#v", greet.Location.Position, greetDefPos)
	}
}

// TestDriver_DocumentSymbolsDropsChildrenWhenProfileSet is the driver-level
// half of the pyright parameter-child criterion: the same hierarchical
// documentSymbol result flattens without its nested children when the
// server's own ServerProfile sets DropSymbolChildren, which is how each
// language's profile reaches the multiplexing MCP server — per driver, not
// per router.
func TestDriver_DocumentSymbolsDropsChildrenWhenProfileSet(t *testing.T) {
	d := startTestDriverWithProfile(t, nil, ServerProfile{DropSymbolChildren: true})

	got, err := d.DocumentSymbols(context.Background(), fixtureFile)
	if err != nil {
		t.Fatalf("DocumentSymbols() error = %v", err)
	}

	names := make([]string, len(got))
	for i, s := range got {
		names[i] = s.Name
	}
	wantNames := []string{"Greeter", "EnglishGreeter", "Greet", "Caller"}
	if len(names) != len(wantNames) {
		t.Fatalf("DocumentSymbols() names = %v, want %v (parameter child dropped)", names, wantNames)
	}
	for i, want := range wantNames {
		if names[i] != want {
			t.Errorf("DocumentSymbols()[%d].Name = %q, want %q", i, names[i], want)
		}
	}
}

func TestDriver_WorkspaceSymbols(t *testing.T) {
	d := startTestDriver(t, nil)

	got, err := d.WorkspaceSymbols(context.Background(), "Gr")
	if err != nil {
		t.Fatalf("WorkspaceSymbols() error = %v", err)
	}

	if len(got) != 2 || got[0].Name != "Greet" || got[1].Name != "Caller" {
		t.Fatalf("WorkspaceSymbols() = %#v, want [Greet Caller]", got)
	}
}

func TestDriver_CallHierarchy(t *testing.T) {
	d := startTestDriver(t, nil)

	got, err := d.CallHierarchy(context.Background(), fixtureFile, callerDefPos)
	if err != nil {
		t.Fatalf("CallHierarchy() error = %v", err)
	}

	if got.Item.Name != "Caller" {
		t.Errorf("CallHierarchy().Item.Name = %q, want %q", got.Item.Name, "Caller")
	}
	if len(got.Callers) != 0 {
		t.Errorf("CallHierarchy().Callers = %#v, want none (nothing calls Caller in the fixture)", got.Callers)
	}
	if len(got.Callees) != 1 || got.Callees[0].Name != "Greet" {
		t.Fatalf("CallHierarchy().Callees = %#v, want [Greet]", got.Callees)
	}
	if got.Callees[0].Location.Position != greetDefPos {
		t.Errorf("CallHierarchy().Callees[0].Location.Position = %#v, want %#v", got.Callees[0].Location.Position, greetDefPos)
	}
}

func TestDriver_TypeHierarchy(t *testing.T) {
	d := startTestDriver(t, nil)

	got, err := d.TypeHierarchy(context.Background(), fixtureFile, englishGreeterDefPos)
	if err != nil {
		t.Fatalf("TypeHierarchy() error = %v", err)
	}

	if got.Item.Name != "EnglishGreeter" {
		t.Errorf("TypeHierarchy().Item.Name = %q, want %q", got.Item.Name, "EnglishGreeter")
	}
	if len(got.Supertypes) != 1 || got.Supertypes[0].Name != "Greeter" {
		t.Fatalf("TypeHierarchy().Supertypes = %#v, want [Greeter]", got.Supertypes)
	}
	if got.Supertypes[0].Location.Position != greeterDefPos {
		t.Errorf("TypeHierarchy().Supertypes[0].Location.Position = %#v, want %#v", got.Supertypes[0].Location.Position, greeterDefPos)
	}
	if len(got.Subtypes) != 0 {
		t.Errorf("TypeHierarchy().Subtypes = %#v, want none", got.Subtypes)
	}
}

func TestDriver_DidOpenIsLazyAndPerFile(t *testing.T) {
	logPath, opens := openLog(t)
	d := startTestDriver(t, []string{"FAKEGOPLS_OPEN_LOG=" + logPath})

	ctx := context.Background()
	if _, err := d.FindDefinition(ctx, fixtureFile, greetCallPos); err != nil {
		t.Fatalf("FindDefinition() error = %v", err)
	}
	if _, err := d.FindReferences(ctx, fixtureFile, greetDefPos); err != nil {
		t.Fatalf("FindReferences() error = %v", err)
	}
	if _, err := d.DocumentSymbols(ctx, fixtureFile); err != nil {
		t.Fatalf("DocumentSymbols() error = %v", err)
	}

	// didOpen is a fire-and-forget LSP notification: ensureOpen sends it and
	// issues the query request without awaiting it, so the fake server's
	// append to the open log can lag the synchronous query responses (it is
	// dispatched independently of the request replies). Reading the log once,
	// immediately, therefore races that notification — the source of this
	// test's intermittent CI failures. The Driver's per-file lazy-open cache
	// guarantees at most one didOpen for greet.go across the three calls, so
	// poll for it to land rather than racing it; a count above one is a real
	// regression (didOpen sent per call), which the guard below still catches.
	var got []string
	deadline := time.Now().Add(2 * time.Second)
	for {
		got = opens()
		if len(got) > 1 {
			t.Fatalf("didOpen sent %d times across 3 query calls referencing the same file, want exactly 1: %v", len(got), got)
		}
		if len(got) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("didOpen not observed within 2s after 3 query calls, want exactly 1: %v", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.HasSuffix(got[0], "greet.go") {
		t.Errorf("didOpen URI = %q, want it to name greet.go", got[0])
	}
}

func TestDriver_QueryBeforeStartReturnsErrInert(t *testing.T) {
	d := New(Options{Command: []string{fakeGoplsPath}, Dir: t.TempDir(), ReadinessTimeout: time.Second})

	if _, err := d.FindDefinition(context.Background(), fixtureFile, greetDefPos); err != ErrInert {
		t.Fatalf("FindDefinition() before Start error = %v, want ErrInert", err)
	}
}

func TestDriver_HierarchyMethodsRequireCapability(t *testing.T) {
	d := New(Options{Command: []string{fakeGoplsPath}, Dir: t.TempDir(), ReadinessTimeout: time.Second})

	if _, err := d.CallHierarchy(context.Background(), fixtureFile, callerDefPos); err != ErrCapabilityUnsupported {
		t.Errorf("CallHierarchy() with no declared capability error = %v, want ErrCapabilityUnsupported", err)
	}
	if _, err := d.TypeHierarchy(context.Background(), fixtureFile, englishGreeterDefPos); err != ErrCapabilityUnsupported {
		t.Errorf("TypeHierarchy() with no declared capability error = %v, want ErrCapabilityUnsupported", err)
	}
}

func TestDriver_FindImplementationsRequiresCapability(t *testing.T) {
	d := New(Options{Command: []string{fakeGoplsPath}, Dir: t.TempDir(), ReadinessTimeout: time.Second})

	if _, err := d.FindImplementations(context.Background(), fixtureFile, greeterDefPos); err != ErrCapabilityUnsupported {
		t.Errorf("FindImplementations() with no declared capability error = %v, want ErrCapabilityUnsupported", err)
	}
}

func TestDriver_FindImplementationsUnchangedWhenCapabilityAdvertised(t *testing.T) {
	d := startTestDriver(t, nil)

	got, err := d.FindImplementations(context.Background(), fixtureFile, greeterDefPos)
	if err != nil {
		t.Fatalf("FindImplementations() error = %v", err)
	}

	abs, _ := filepath.Abs(fixtureFile)
	want := []Location{{File: abs, Position: englishGreeterDefPos}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("FindImplementations() = %#v, want %#v", got, want)
	}
}

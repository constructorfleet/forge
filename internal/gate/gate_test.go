package gate_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/gate"
)

// fakeCommandRunner is a deterministic gate.CommandRunner double: outcomes
// are programmed per command string, so tests never shell out to a real
// tool.
type fakeCommandRunner struct {
	outcomes map[string]fakeOutcome
	calls    []string
}

type fakeOutcome struct {
	exitCode int
	stdout   string
	stderr   string
	err      error
}

func (f *fakeCommandRunner) Run(_ context.Context, _, command string, stdout, stderr io.Writer) (int, error) {
	f.calls = append(f.calls, command)
	oc, ok := f.outcomes[command]
	if !ok {
		return 0, nil
	}
	_, _ = io.WriteString(stdout, oc.stdout)
	_, _ = io.WriteString(stderr, oc.stderr)
	return oc.exitCode, oc.err
}

func TestRun_ExecutesGatesInConfiguredOrder(t *testing.T) {
	fake := &fakeCommandRunner{outcomes: map[string]fakeOutcome{}}
	r := gate.NewRunner(fake)

	gates := []config.QualityGate{
		{Name: "test", Command: "make test"},
		{Name: "lint", Command: "make lint"},
		{Name: "build", Command: "make build"},
	}
	results := r.Run(context.Background(), "/work", gates, gate.Options{})

	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	wantOrder := []string{"make test", "make lint", "make build"}
	if len(fake.calls) != 3 {
		t.Fatalf("got %d calls, want 3", len(fake.calls))
	}
	for i, want := range wantOrder {
		if fake.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, fake.calls[i], want)
		}
		if results[i].Name != gates[i].Name || results[i].Command != gates[i].Command {
			t.Errorf("results[%d] = %+v, want Name/Command %s/%s", i, results[i], gates[i].Name, gates[i].Command)
		}
	}
}

func TestRun_RecordsNameCommandTimingExitCodeStdoutStderr(t *testing.T) {
	fake := &fakeCommandRunner{outcomes: map[string]fakeOutcome{
		"make lint": {exitCode: 1, stdout: "some output", stderr: "some problem"},
	}}
	r := gate.NewRunner(fake)
	r.Now = stepClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Second)

	results := r.Run(context.Background(), "/work", []config.QualityGate{
		{Name: "lint", Command: "make lint"},
	}, gate.Options{})

	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	res := results[0]
	if res.Name != "lint" || res.Command != "make lint" {
		t.Errorf("Name/Command = %s/%s", res.Name, res.Command)
	}
	if res.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", res.ExitCode)
	}
	if res.Stdout != "some output" {
		t.Errorf("Stdout = %q, want %q", res.Stdout, "some output")
	}
	if res.Stderr != "some problem" {
		t.Errorf("Stderr = %q, want %q", res.Stderr, "some problem")
	}
	if res.Passed {
		t.Error("Passed = true, want false for a non-zero exit code")
	}
	if !res.FinishedAt.After(res.StartedAt) {
		t.Errorf("FinishedAt (%v) not after StartedAt (%v)", res.FinishedAt, res.StartedAt)
	}
}

func TestRun_PassingGateHasZeroExitAndPassedTrue(t *testing.T) {
	fake := &fakeCommandRunner{outcomes: map[string]fakeOutcome{
		"make test": {exitCode: 0, stdout: "ok"},
	}}
	r := gate.NewRunner(fake)

	results := r.Run(context.Background(), "/work", []config.QualityGate{
		{Name: "test", Command: "make test"},
	}, gate.Options{})

	if !results[0].Passed {
		t.Error("Passed = false, want true for exit code 0")
	}
}

func TestRun_FirstFailureStopsSubsequentGatesByDefault(t *testing.T) {
	fake := &fakeCommandRunner{outcomes: map[string]fakeOutcome{
		"make test": {exitCode: 1},
	}}
	r := gate.NewRunner(fake)

	gates := []config.QualityGate{
		{Name: "test", Command: "make test"},
		{Name: "lint", Command: "make lint"},
	}
	results := r.Run(context.Background(), "/work", gates, gate.Options{})

	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (stop after first failure)", len(results))
	}
	if len(fake.calls) != 1 {
		t.Fatalf("got %d calls, want 1 (lint should not have run)", len(fake.calls))
	}
}

func TestRun_ContinueOnFailureRunsAllGates(t *testing.T) {
	fake := &fakeCommandRunner{outcomes: map[string]fakeOutcome{
		"make test": {exitCode: 1},
	}}
	r := gate.NewRunner(fake)

	gates := []config.QualityGate{
		{Name: "test", Command: "make test"},
		{Name: "lint", Command: "make lint"},
	}
	results := r.Run(context.Background(), "/work", gates, gate.Options{ContinueOnFailure: true})

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if len(fake.calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(fake.calls))
	}
}

func TestRun_OutputBoundedToMaxBytes(t *testing.T) {
	fake := &fakeCommandRunner{outcomes: map[string]fakeOutcome{
		"make test": {exitCode: 1, stdout: "0123456789", stderr: "abcdefghij"},
	}}
	r := gate.NewRunner(fake)

	results := r.Run(context.Background(), "/work", []config.QualityGate{
		{Name: "test", Command: "make test"},
	}, gate.Options{MaxOutputBytes: 4})

	res := results[0]
	if len(res.Stdout) > 4+len("... (head truncated, showing tail)\n") {
		t.Errorf("Stdout not bounded: %q (len %d)", res.Stdout, len(res.Stdout))
	}
	if got, want := res.Stdout[len(res.Stdout)-4:], "6789"; got != want {
		t.Errorf("Stdout tail = %q, want %q (tail-preserving)", got, want)
	}
	if got, want := res.Stderr[len(res.Stderr)-4:], "ghij"; got != want {
		t.Errorf("Stderr tail = %q, want %q (tail-preserving)", got, want)
	}
}

func TestRun_ZeroMaxOutputBytesIsUnbounded(t *testing.T) {
	fake := &fakeCommandRunner{outcomes: map[string]fakeOutcome{
		"make test": {exitCode: 0, stdout: "0123456789"},
	}}
	r := gate.NewRunner(fake)

	results := r.Run(context.Background(), "/work", []config.QualityGate{
		{Name: "test", Command: "make test"},
	}, gate.Options{})

	if results[0].Stdout != "0123456789" {
		t.Errorf("Stdout = %q, want unbounded %q", results[0].Stdout, "0123456789")
	}
}

func TestRun_CommandRunnerErrorRecordedAsFailure(t *testing.T) {
	fake := &fakeCommandRunner{outcomes: map[string]fakeOutcome{
		"make test": {err: errors.New("exec: \"make\": executable file not found in $PATH")},
	}}
	r := gate.NewRunner(fake)

	results := r.Run(context.Background(), "/work", []config.QualityGate{
		{Name: "test", Command: "make test"},
	}, gate.Options{})

	res := results[0]
	if res.Passed {
		t.Error("Passed = true, want false when CommandRunner errors")
	}
	if res.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1", res.ExitCode)
	}
}

func TestRun_NoGatesReturnsEmptyResults(t *testing.T) {
	fake := &fakeCommandRunner{outcomes: map[string]fakeOutcome{}}
	r := gate.NewRunner(fake)

	results := r.Run(context.Background(), "/work", nil, gate.Options{})
	if len(results) != 0 {
		t.Fatalf("got %d results, want 0", len(results))
	}
}

// stepClock returns a func() time.Time that advances by step on every call,
// starting at start, so tests can assert FinishedAt strictly follows
// StartedAt without depending on real wall-clock granularity.
func stepClock(start time.Time, step time.Duration) func() time.Time {
	next := start
	return func() time.Time {
		t := next
		next = next.Add(step)
		return t
	}
}

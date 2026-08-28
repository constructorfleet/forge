package gate_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/gate"
	"github.com/Teagan42/forge/internal/gate/gatetest"
)

func TestRun_ExecutesGatesInConfiguredOrder(t *testing.T) {
	fake := gatetest.NewFakeCommandRunner()
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
	calls := fake.Calls()
	if len(calls) != 3 {
		t.Fatalf("got %d calls, want 3", len(calls))
	}
	for i, want := range wantOrder {
		if calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, calls[i], want)
		}
		if results[i].Name != gates[i].Name || results[i].Command != gates[i].Command {
			t.Errorf("results[%d] = %+v, want Name/Command %s/%s", i, results[i], gates[i].Name, gates[i].Command)
		}
	}
}

func TestRun_RecordsNameCommandTimingExitCodeStdoutStderr(t *testing.T) {
	fake := gatetest.NewFakeCommandRunner()
	fake.ProgramResult("make lint", 1, "some output", "some problem")
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
	fake := gatetest.NewFakeCommandRunner()
	fake.ProgramResult("make test", 0, "ok", "")
	r := gate.NewRunner(fake)

	results := r.Run(context.Background(), "/work", []config.QualityGate{
		{Name: "test", Command: "make test"},
	}, gate.Options{})

	if !results[0].Passed {
		t.Error("Passed = false, want true for exit code 0")
	}
}

func TestRun_FirstFailureStopsSubsequentGatesByDefault(t *testing.T) {
	fake := gatetest.NewFakeCommandRunner()
	fake.ProgramResult("make test", 1, "", "")
	r := gate.NewRunner(fake)

	gates := []config.QualityGate{
		{Name: "test", Command: "make test"},
		{Name: "lint", Command: "make lint"},
	}
	results := r.Run(context.Background(), "/work", gates, gate.Options{})

	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (stop after first failure)", len(results))
	}
	if calls := fake.Calls(); len(calls) != 1 {
		t.Fatalf("got %d calls, want 1 (lint should not have run)", len(calls))
	}
}

func TestRun_ContinueOnFailureRunsAllGates(t *testing.T) {
	fake := gatetest.NewFakeCommandRunner()
	fake.ProgramResult("make test", 1, "", "")
	r := gate.NewRunner(fake)

	gates := []config.QualityGate{
		{Name: "test", Command: "make test"},
		{Name: "lint", Command: "make lint"},
	}
	results := r.Run(context.Background(), "/work", gates, gate.Options{ContinueOnFailure: true})

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if calls := fake.Calls(); len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
}

func TestRun_OutputBoundedToMaxBytes(t *testing.T) {
	fake := gatetest.NewFakeCommandRunner()
	fake.ProgramResult("make test", 1, "0123456789", "abcdefghij")
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
	fake := gatetest.NewFakeCommandRunner()
	fake.ProgramResult("make test", 0, "0123456789", "")
	r := gate.NewRunner(fake)

	results := r.Run(context.Background(), "/work", []config.QualityGate{
		{Name: "test", Command: "make test"},
	}, gate.Options{})

	if results[0].Stdout != "0123456789" {
		t.Errorf("Stdout = %q, want unbounded %q", results[0].Stdout, "0123456789")
	}
}

func TestRun_CommandRunnerErrorRecordedAsFailure(t *testing.T) {
	fake := gatetest.NewFakeCommandRunner()
	fake.ProgramError("make test", errors.New("exec: \"make\": executable file not found in $PATH"))
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
	fake := gatetest.NewFakeCommandRunner()
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

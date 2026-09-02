package container_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/execution/container"
)

func TestDetectCLIRuntime_PicksFirstRespondingBinary(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{{exitCode: 0}}}

	rt, err := container.DetectCLIRuntime(context.Background(), runner)
	if err != nil {
		t.Fatalf("DetectCLIRuntime: %v", err)
	}
	if rt == nil {
		t.Fatal("DetectCLIRuntime: runtime is nil")
	}

	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(runner.calls))
	}
	if runner.calls[0][0] != "docker" {
		t.Errorf("probed binary = %q, want docker", runner.calls[0][0])
	}
}

func TestDetectCLIRuntime_FallsBackWhenFirstBinaryUnreachable(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{
		{err: errors.New("docker: command not found")},
		{exitCode: 0},
	}}

	rt, err := container.DetectCLIRuntime(context.Background(), runner)
	if err != nil {
		t.Fatalf("DetectCLIRuntime: %v", err)
	}
	if rt == nil {
		t.Fatal("DetectCLIRuntime: runtime is nil")
	}

	if len(runner.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(runner.calls))
	}
	if runner.calls[1][0] != "podman" {
		t.Errorf("second probed binary = %q, want podman", runner.calls[1][0])
	}
}

func TestDetectCLIRuntime_FallsBackWhenFirstDaemonUnreachable(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{
		{stderr: "Cannot connect to the Docker daemon", exitCode: 1},
		{exitCode: 0},
	}}

	rt, err := container.DetectCLIRuntime(context.Background(), runner)
	if err != nil {
		t.Fatalf("DetectCLIRuntime: %v", err)
	}
	if rt == nil {
		t.Fatal("DetectCLIRuntime: runtime is nil")
	}
}

func TestDetectCLIRuntime_GivesEachCandidateItsOwnSubTimeout(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{
		{err: errors.New("docker: no response")},
		{exitCode: 0},
	}}
	parentCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := container.DetectCLIRuntime(parentCtx, runner)
	if err != nil {
		t.Fatalf("DetectCLIRuntime: %v", err)
	}

	if len(runner.ctxs) != 2 {
		t.Fatalf("ctxs = %d, want 2", len(runner.ctxs))
	}
	firstDeadline, ok := runner.ctxs[0].Deadline()
	if !ok {
		t.Fatal("first probe context: want a deadline, got none")
	}
	secondDeadline, ok := runner.ctxs[1].Deadline()
	if !ok {
		t.Fatal("second probe context: want a deadline, got none")
	}
	if firstDeadline.Equal(secondDeadline) {
		t.Errorf("both probe contexts share deadline %v, want independent per-candidate sub-deadlines", firstDeadline)
	}
	parentDeadline, _ := parentCtx.Deadline()
	if !firstDeadline.Before(parentDeadline) {
		t.Errorf("first probe deadline %v, want it before the parent deadline %v so the second candidate keeps a share of the budget", firstDeadline, parentDeadline)
	}
}

func TestDetectCLIRuntime_ReturnsErrRuntimeUnavailableWhenNoneRespond(t *testing.T) {
	runner := &fakeCommandRunner{results: []fakeResult{
		{err: errors.New("docker: command not found")},
		{stderr: "Cannot connect to the Podman daemon", exitCode: 1},
	}}

	_, err := container.DetectCLIRuntime(context.Background(), runner)
	if !errors.Is(err, container.ErrRuntimeUnavailable) {
		t.Fatalf("DetectCLIRuntime: err = %v, want wrapping ErrRuntimeUnavailable", err)
	}
}

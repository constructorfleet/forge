package main

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/steering"
)

// fakeSteerer is a test double for the steerer seam doRunSteer enqueues
// through, so its argument parsing and output can be verified without a real
// running loop.
type fakeSteerer struct {
	gotLoopID string
	gotText   string
	err       error
}

func (f *fakeSteerer) Steer(loopID, text string) error {
	f.gotLoopID = loopID
	f.gotText = text
	return f.err
}

func TestDoRunSteer_MissingArgs_ReportsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	fake := &fakeSteerer{}

	code := doRunSteer([]string{"loop-1"}, fake, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("doRunSteer() = %d, want 2", code)
	}
	if fake.gotLoopID != "" {
		t.Fatalf("Steer() called with missing args, want no call")
	}
}

func TestDoRunSteer_QueuesMessageAndReportsQueued(t *testing.T) {
	var stdout, stderr bytes.Buffer
	fake := &fakeSteerer{}

	code := doRunSteer([]string{"loop-1", "please", "add", "a", "retry"}, fake, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("doRunSteer() = %d, want 0; stderr: %s", code, stderr.String())
	}
	if fake.gotLoopID != "loop-1" {
		t.Fatalf("Steer() loopID = %q, want %q", fake.gotLoopID, "loop-1")
	}
	if fake.gotText != "please add a retry" {
		t.Fatalf("Steer() text = %q, want %q", fake.gotText, "please add a retry")
	}
	if !bytes.Contains(stdout.Bytes(), []byte("queued")) {
		t.Fatalf("stdout = %q, want it to report the message as queued", stdout.String())
	}
	if bytes.Contains(stdout.Bytes(), []byte("applied")) {
		t.Fatalf("stdout = %q, must not imply immediate application", stdout.String())
	}
}

func TestDoRunSteer_UnknownLoop_ReportsError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	fake := &fakeSteerer{err: steering.ErrLoopNotFound}

	code := doRunSteer([]string{"no-such-loop", "hello"}, fake, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("doRunSteer() = %d, want 1", code)
	}
	if !errors.Is(fake.err, steering.ErrLoopNotFound) {
		t.Fatalf("expected the fake to still carry ErrLoopNotFound")
	}
	if stderr.Len() == 0 {
		t.Fatalf("stderr is empty, want an error message")
	}
}

// TestDoRunSteer_ReturnsWithoutWaitingForStepCompletion proves the CLI-level
// acceptance criterion: submitting a message while a step is in progress
// returns a queued acknowledgement without waiting for step completion. It
// wires doRunSteer directly to a real steering.Registry and Queue, with a
// simulated in-progress step, mirroring
// TestRegistry_SteerDuringInProgressStepReturnsWithoutWaiting one level up.
func TestDoRunSteer_ReturnsWithoutWaitingForStepCompletion(t *testing.T) {
	registry := steering.NewRegistry()
	queue := steering.NewQueue()
	registry.Register("loop-1", queue, nil)

	stepDone := make(chan struct{})
	stepStarted := make(chan struct{})
	go func() {
		close(stepStarted)
		<-stepDone
	}()
	<-stepStarted
	defer close(stepDone)

	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- doRunSteer([]string{"loop-1", "steer", "mid-step"}, registry, &stdout, &stderr)
	}()

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("doRunSteer() = %d, want 0; stderr: %s", code, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("doRunSteer() blocked instead of returning immediately while the step was in progress")
	}

	if drained := queue.DrainAll(); drained != "steer mid-step" {
		t.Fatalf("queue.DrainAll() = %q, want %q", drained, "steer mid-step")
	}
}

package steering_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/executeloop"
	"github.com/Teagan42/forge/internal/steering"
)

func TestRegistry_SteerEnqueuesOntoRegisteredQueue(t *testing.T) {
	r := steering.NewRegistry()
	q := steering.NewQueue()
	r.Register("loop-1", q, nil)

	if err := r.Steer("loop-1", "steer this way"); err != nil {
		t.Fatalf("Steer() error = %v, want nil", err)
	}

	got := q.Drain()
	want := []steering.Message{{Text: "steer this way"}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("queue.Drain() = %+v, want %+v", got, want)
	}
}

func TestRegistry_SteerOnUnknownLoopReturnsErrLoopNotFound(t *testing.T) {
	r := steering.NewRegistry()

	err := r.Steer("no-such-loop", "hello")

	if !errors.Is(err, steering.ErrLoopNotFound) {
		t.Fatalf("Steer() error = %v, want wrapping ErrLoopNotFound", err)
	}
}

func TestRegistry_UnregisterRemovesLoop(t *testing.T) {
	r := steering.NewRegistry()
	q := steering.NewQueue()
	r.Register("loop-1", q, nil)

	r.Unregister("loop-1")

	err := r.Steer("loop-1", "too late")
	if !errors.Is(err, steering.ErrLoopNotFound) {
		t.Fatalf("Steer() after Unregister() error = %v, want ErrLoopNotFound", err)
	}
}

// TestRegistry_UnregisterKeepsLoopWhileOtherRegistrationOutstanding proves
// the fix for constructorfleet/forge#746's concurrent-Workers gap:
// ExecuteInExecution registers the same Execution ID once per concurrent
// Worker sharing that Execution, so one Worker's Unregister must not evict
// the loop-id while a sibling Worker's registration is still outstanding.
func TestRegistry_UnregisterKeepsLoopWhileOtherRegistrationOutstanding(t *testing.T) {
	r := steering.NewRegistry()
	q := steering.NewQueue()
	r.Register("loop-1", q, nil)
	r.Register("loop-1", q, nil)

	r.Unregister("loop-1")

	if err := r.Steer("loop-1", "still running"); err != nil {
		t.Fatalf("Steer() after one of two Unregister() calls error = %v, want nil", err)
	}

	r.Unregister("loop-1")

	err := r.Steer("loop-1", "too late")
	if !errors.Is(err, steering.ErrLoopNotFound) {
		t.Fatalf("Steer() after both Unregister() calls error = %v, want ErrLoopNotFound", err)
	}
}

// TestRegistry_SteerDuringInProgressStepReturnsWithoutWaiting proves the CLI/API
// entry point's acceptance criterion: submitting a message while a step is in
// progress returns a queued acknowledgement without waiting for step
// completion. It mirrors TestQueue_ConcurrentEnqueueDuringSimulatedStep in
// queue_test.go, one level up through the Registry.
func TestRegistry_SteerDuringInProgressStepReturnsWithoutWaiting(t *testing.T) {
	r := steering.NewRegistry()
	q := steering.NewQueue()
	r.Register("loop-1", q, nil)

	stepDone := make(chan struct{})
	stepStarted := make(chan struct{})
	go func() {
		close(stepStarted)
		<-stepDone
	}()
	<-stepStarted
	defer close(stepDone)

	done := make(chan error, 1)
	go func() { done <- r.Steer("loop-1", "steer mid-step") }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Steer() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Steer() blocked instead of returning immediately while the step was in progress")
	}
}

// TestRegistry_StatusReflectsRegisteredSessionTransitions proves TKT-009's
// acceptance criterion: a caller resolves a running loop's status through the
// same Registry `forge steer` already resolves loop-ids through, and that
// status changes from running to needs_info and back as the underlying
// executeloop.Session transitions.
func TestRegistry_StatusReflectsRegisteredSessionTransitions(t *testing.T) {
	r := steering.NewRegistry()
	q := steering.NewQueue()
	session := executeloop.NewSession()
	r.Register("loop-1", q, session)

	got, err := r.Status("loop-1")
	if err != nil {
		t.Fatalf("Status() error = %v, want nil", err)
	}
	if got != executeloop.StatusRunning {
		t.Fatalf("Status() = %q, want %q", got, executeloop.StatusRunning)
	}

	session.SetStatus(executeloop.StatusNeedsInfo)

	got, err = r.Status("loop-1")
	if err != nil {
		t.Fatalf("Status() error = %v, want nil", err)
	}
	if got != executeloop.StatusNeedsInfo {
		t.Fatalf("Status() = %q, want %q", got, executeloop.StatusNeedsInfo)
	}

	session.SetStatus(executeloop.StatusRunning)

	got, err = r.Status("loop-1")
	if err != nil {
		t.Fatalf("Status() error = %v, want nil", err)
	}
	if got != executeloop.StatusRunning {
		t.Fatalf("Status() = %q, want %q", got, executeloop.StatusRunning)
	}
}

func TestRegistry_StatusOnUnknownLoopReturnsErrLoopNotFound(t *testing.T) {
	r := steering.NewRegistry()

	_, err := r.Status("no-such-loop")

	if !errors.Is(err, steering.ErrLoopNotFound) {
		t.Fatalf("Status() error = %v, want wrapping ErrLoopNotFound", err)
	}
}

// TestRegistry_StatusOnQueueOnlyRegistrationReturnsErrStatusUnavailable
// covers a Register call made with a nil Session (an Engine with no
// executeloop.Session wired, e.g. Steering configured but Session not yet
// set): Status must distinguish "no such loop" from "loop known, but its
// live status isn't tracked" rather than conflating the two.
func TestRegistry_StatusOnQueueOnlyRegistrationReturnsErrStatusUnavailable(t *testing.T) {
	r := steering.NewRegistry()
	q := steering.NewQueue()
	r.Register("loop-1", q, nil)

	_, err := r.Status("loop-1")

	if !errors.Is(err, steering.ErrStatusUnavailable) {
		t.Fatalf("Status() error = %v, want wrapping ErrStatusUnavailable", err)
	}
}

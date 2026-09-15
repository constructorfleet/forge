package steering_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/steering"
)

func TestRegistry_SteerEnqueuesOntoRegisteredQueue(t *testing.T) {
	r := steering.NewRegistry()
	q := steering.NewQueue()
	r.Register("loop-1", q)

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
	r.Register("loop-1", q)

	r.Unregister("loop-1")

	err := r.Steer("loop-1", "too late")
	if !errors.Is(err, steering.ErrLoopNotFound) {
		t.Fatalf("Steer() after Unregister() error = %v, want ErrLoopNotFound", err)
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
	r.Register("loop-1", q)

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

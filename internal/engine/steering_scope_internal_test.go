package engine

import (
	"testing"

	"github.com/Teagan42/forge/internal/executeloop"
	"github.com/Teagan42/forge/internal/steering"
)

func TestSteeringSessionIsScopedToExecution(t *testing.T) {
	e := &Engine{Steering: steering.NewQueue(), Session: executeloop.NewSession()}
	_, releaseFirst := e.acquireSteeringExecution("execution-1")
	defer releaseFirst()
	_, releaseSecond := e.acquireSteeringExecution("execution-2")
	defer releaseSecond()

	first := e.steeringSession("execution-1")
	second := e.steeringSession("execution-2")
	if first == nil || second == nil {
		t.Fatal("each execution must have a steering session")
	}
	if first == second {
		t.Fatal("concurrent executions must not share a steering session")
	}
	first.SetStatus(executeloop.StatusNeedsInfo)
	if got := second.Status(); got != executeloop.StatusRunning {
		t.Fatalf("second execution status = %q, want %q", got, executeloop.StatusRunning)
	}
}

func TestSteeringQueueIsScopedToExecution(t *testing.T) {
	configured := steering.NewQueue()
	e := &Engine{Steering: configured}

	first, releaseFirst := e.acquireSteeringQueue("execution-1")
	defer releaseFirst()
	second, releaseSecond := e.acquireSteeringQueue("execution-2")
	defer releaseSecond()

	if first == configured {
		t.Fatal("execution must receive a queue owned by its execution")
	}
	if second == first {
		t.Fatal("concurrent executions must not share a steering queue")
	}
}

func TestSteeringQueueIsNotReusedAfterRelease(t *testing.T) {
	configured := steering.NewQueue()
	e := &Engine{Steering: configured}

	first, releaseFirst := e.acquireSteeringQueue("execution-1")
	releaseFirst()
	configured.Enqueue(steering.Message{Text: "stale"})

	second, releaseSecond := e.acquireSteeringQueue("execution-2")
	defer releaseSecond()
	if second == configured || second == first {
		t.Fatal("later execution must receive a fresh queue")
	}
	if got := second.DrainAll(); got != "" {
		t.Fatalf("later execution received stale message %q", got)
	}
}

func TestSteeringQueueLookupDoesNotFallbackToCompatibilityQueue(t *testing.T) {
	configured := steering.NewQueue()
	e := &Engine{Steering: configured}

	if got := e.steeringQueue("unknown-execution"); got != nil {
		t.Fatalf("unknown execution must not resolve the compatibility queue, got %p", got)
	}
}

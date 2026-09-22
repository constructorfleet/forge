package engine

import (
	"testing"

	"github.com/Teagan42/forge/internal/steering"
)

func TestSteeringQueueIsScopedToExecution(t *testing.T) {
	configured := steering.NewQueue()
	e := &Engine{Steering: configured}

	first, releaseFirst := e.acquireSteeringQueue("execution-1")
	defer releaseFirst()
	second, releaseSecond := e.acquireSteeringQueue("execution-2")
	defer releaseSecond()

	if first != configured {
		t.Fatal("first execution must preserve the configured queue compatibility path")
	}
	if second == first {
		t.Fatal("concurrent executions must not share a steering queue")
	}
}

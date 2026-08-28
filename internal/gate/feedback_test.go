package gate_test

import (
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/gate"
)

func TestBuildFeedback_IncludesGateNameCommandExitCodeAndOutput(t *testing.T) {
	res := gate.Result{
		Name:     "lint",
		Command:  "make lint",
		ExitCode: 1,
		Stdout:   "3 problems found",
		Stderr:   "eslint: config error",
		Passed:   false,
	}

	fb := gate.BuildFeedback(res)

	if fb.Source != agent.FeedbackSourceGate {
		t.Errorf("Source = %s, want %s", fb.Source, agent.FeedbackSourceGate)
	}
	for _, want := range []string{"lint", "make lint", "1", "3 problems found", "eslint: config error"} {
		if !strings.Contains(fb.Message, want) {
			t.Errorf("Message = %q, want it to contain %q", fb.Message, want)
		}
	}
}

func TestBuildFeedback_OmitsEmptyStreams(t *testing.T) {
	res := gate.Result{Name: "build", Command: "make build", ExitCode: 2}

	fb := gate.BuildFeedback(res)

	if strings.Contains(fb.Message, "Stdout:") {
		t.Errorf("Message = %q, want no Stdout section when empty", fb.Message)
	}
	if strings.Contains(fb.Message, "Stderr:") {
		t.Errorf("Message = %q, want no Stderr section when empty", fb.Message)
	}
}

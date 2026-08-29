package claude

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
)

func TestExecute_PropagatesOptionalTokenUsage(t *testing.T) {
	adapter := &Adapter{
		Runner: func(context.Context, string, []string, string, []string, func(string)) (string, string, int, error) {
			return "```json\n" +
				`{"status":"IMPLEMENTED","summary":"done","usage":{"input_tokens":12,"output_tokens":3}}` +
				"\n```\n", "", 0, nil
		},
	}

	result, err := adapter.Execute(context.Background(), agent.AgentRequest{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Usage == nil {
		t.Fatal("Usage = nil, want token usage")
	}
	if result.Usage.InputTokens != 12 || result.Usage.OutputTokens != 3 {
		t.Fatalf("Usage = %+v, want input=12 output=3", result.Usage)
	}
}

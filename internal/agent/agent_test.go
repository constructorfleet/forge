package agent

import (
	"encoding/json"
	"testing"
)

func TestModeStructuredIsDistinctFromExistingModes(t *testing.T) {
	if ModeStructured == ModeImplement {
		t.Fatalf("ModeStructured must not equal the zero-value ModeImplement")
	}
	if ModeStructured == ModeReview {
		t.Fatalf("ModeStructured must not equal ModeReview")
	}
}

func TestAgentRequestPromptAndSchemaRoundTripThroughJSON(t *testing.T) {
	req := AgentRequest{
		Mode:   ModeStructured,
		Prompt: "summarize the diff in one sentence",
		Schema: `{"type":"object","properties":{"summary":{"type":"string"}}}`,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal(req): %v", err)
	}

	var got AgentRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if got.Prompt != req.Prompt {
		t.Errorf("Prompt = %q, want %q", got.Prompt, req.Prompt)
	}
	if got.Schema != req.Schema {
		t.Errorf("Schema = %q, want %q", got.Schema, req.Schema)
	}
	if got.Mode != ModeStructured {
		t.Errorf("Mode = %q, want %q", got.Mode, ModeStructured)
	}
}

func TestAgentRequestZeroValuePromptAndSchemaAreEmpty(t *testing.T) {
	var req AgentRequest

	if req.Prompt != "" {
		t.Errorf("zero-value Prompt = %q, want empty string", req.Prompt)
	}
	if req.Schema != "" {
		t.Errorf("zero-value Schema = %q, want empty string", req.Schema)
	}
	if req.Mode != ModeImplement {
		t.Errorf("zero-value Mode = %q, want ModeImplement", req.Mode)
	}
}

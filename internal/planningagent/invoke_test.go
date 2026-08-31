package planningagent_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Teagan42/forge/internal/planningagent"
)

type draftRequest struct {
	Topic string
}

type draftResult struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

func buildDraftPrompt(req draftRequest) string {
	return fmt.Sprintf("draft something about %s", req.Topic)
}

func TestInvokeStructured_ExtractsLastValidFencedBlock(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("draft-1", "some preamble\n"+
		"```json\n{\"title\":\"wrong\",\"body\":\"stale\"}\n```\n"+
		"more text\n"+
		"```json\n{\"title\":\"Right Title\",\"body\":\"Right body\"}\n```\n")

	res, err := planningagent.InvokeStructured[draftRequest, draftResult](
		context.Background(), backend, "draft-1", draftRequest{Topic: "forge"}, buildDraftPrompt, nil,
	)
	if err != nil {
		t.Fatalf("InvokeStructured: %v", err)
	}
	if res.Title != "Right Title" || res.Body != "Right body" {
		t.Errorf("res = %+v, want the last fenced block decoded", res)
	}

	invocations := backend.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("Invocations() len = %d, want 1", len(invocations))
	}
	if invocations[0].Key != "draft-1" {
		t.Errorf("invocation Key = %q, want draft-1", invocations[0].Key)
	}
	if invocations[0].Prompt != "draft something about forge" {
		t.Errorf("invocation Prompt = %q, want built prompt", invocations[0].Prompt)
	}
}

func TestInvokeStructured_SkipsBlocksFailingValidation(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("draft-2",
		"```json\n{\"title\":\"\",\"body\":\"no title\"}\n```\n"+
			"```json\n{\"title\":\"Has Title\",\"body\":\"ok\"}\n```\n")

	validate := func(r draftResult) error {
		if r.Title == "" {
			return errors.New("title required")
		}
		return nil
	}

	res, err := planningagent.InvokeStructured[draftRequest, draftResult](
		context.Background(), backend, "draft-2", draftRequest{Topic: "forge"}, buildDraftPrompt, validate,
	)
	if err != nil {
		t.Fatalf("InvokeStructured: %v", err)
	}
	if res.Title != "Has Title" {
		t.Errorf("res.Title = %q, want the block passing validation, not an earlier one", res.Title)
	}
}

func TestInvokeStructured_NoValidBlock_FailsPredictably(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("draft-3", "no fenced block here, just prose")

	_, err := planningagent.InvokeStructured[draftRequest, draftResult](
		context.Background(), backend, "draft-3", draftRequest{Topic: "forge"}, buildDraftPrompt, nil,
	)
	if err == nil {
		t.Fatal("InvokeStructured: want error for missing structured result, got nil")
	}
}

func TestInvokeStructured_MalformedJSON_FailsPredictably(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("draft-4", "```json\n{not valid json\n```\n")

	_, err := planningagent.InvokeStructured[draftRequest, draftResult](
		context.Background(), backend, "draft-4", draftRequest{Topic: "forge"}, buildDraftPrompt, nil,
	)
	if err == nil {
		t.Fatal("InvokeStructured: want error for malformed JSON, got nil")
	}
}

func TestInvokeStructured_BackendError_Propagates(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramError("draft-5", errors.New("boom"))

	_, err := planningagent.InvokeStructured[draftRequest, draftResult](
		context.Background(), backend, "draft-5", draftRequest{Topic: "forge"}, buildDraftPrompt, nil,
	)
	if err == nil {
		t.Fatal("InvokeStructured: want error propagated from backend, got nil")
	}
}

func TestInvokeStructured_NilBuild_FailsPredictably(t *testing.T) {
	backend := planningagent.NewFakeBackend()

	_, err := planningagent.InvokeStructured[draftRequest, draftResult](
		context.Background(), backend, "draft-6", draftRequest{Topic: "forge"}, nil, nil,
	)
	if err == nil {
		t.Fatal("InvokeStructured: want error for nil build, got nil")
	}
}

func TestFakeBackend_Invoke_RecordsInvokeRequestFields(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("draft-7", "```json\n{\"title\":\"t\",\"body\":\"b\"}\n```\n")

	raw, err := backend.Invoke(context.Background(), planningagent.InvokeRequest{
		Key:    "draft-7",
		Prompt: "draft something",
		Schema: []byte(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if raw != "```json\n{\"title\":\"t\",\"body\":\"b\"}\n```\n" {
		t.Errorf("Invoke raw = %q, want programmed result", raw)
	}

	invocations := backend.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("Invocations() len = %d, want 1", len(invocations))
	}
	if invocations[0].Key != "draft-7" {
		t.Errorf("invocation Key = %q, want draft-7", invocations[0].Key)
	}
	if invocations[0].Prompt != "draft something" {
		t.Errorf("invocation Prompt = %q, want draft something", invocations[0].Prompt)
	}
	if string(invocations[0].Schema) != `{"type":"object"}` {
		t.Errorf("invocation Schema = %q, want the schema passed in", invocations[0].Schema)
	}
}

func TestFakeBackend_DefaultAndRepeatLast(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramDefault("```json\n{\"title\":\"default\",\"body\":\"d\"}\n```\n")
	backend.ProgramResult("scripted", "```json\n{\"title\":\"first\",\"body\":\"a\"}\n```\n")
	backend.ProgramResult("scripted", "```json\n{\"title\":\"second\",\"body\":\"b\"}\n```\n")

	res, err := planningagent.InvokeStructured[draftRequest, draftResult](
		context.Background(), backend, "unscripted", draftRequest{}, buildDraftPrompt, nil,
	)
	if err != nil {
		t.Fatalf("InvokeStructured: %v", err)
	}
	if res.Title != "default" {
		t.Errorf("res.Title = %q, want default", res.Title)
	}

	for _, want := range []string{"first", "second", "second"} {
		res, err := planningagent.InvokeStructured[draftRequest, draftResult](
			context.Background(), backend, "scripted", draftRequest{}, buildDraftPrompt, nil,
		)
		if err != nil {
			t.Fatalf("InvokeStructured: %v", err)
		}
		if res.Title != want {
			t.Errorf("res.Title = %q, want %q", res.Title, want)
		}
	}
}

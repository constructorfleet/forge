package planningagent_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

func TestInvokeStructured_DecodesBareJSONResult(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("draft-1", `{"title":"Right Title","body":"Right body"}`)

	res, err := planningagent.InvokeStructured[draftRequest, draftResult](
		context.Background(), backend, "draft-1", draftRequest{Topic: "forge"}, buildDraftPrompt, nil,
	)
	if err != nil {
		t.Fatalf("InvokeStructured: %v", err)
	}
	if res.Title != "Right Title" || res.Body != "Right body" {
		t.Errorf("res = %+v, want the bare JSON result decoded", res)
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

func TestInvokeStructured_FailingValidation_FailsPredictably(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("draft-2", `{"title":"","body":"no title"}`)

	validate := func(r draftResult) error {
		if r.Title == "" {
			return errors.New("title required")
		}
		return nil
	}

	_, err := planningagent.InvokeStructured[draftRequest, draftResult](
		context.Background(), backend, "draft-2", draftRequest{Topic: "forge"}, buildDraftPrompt, validate,
	)
	if err == nil {
		t.Fatal("InvokeStructured: want error for result failing validation, got nil")
	}
}

func TestInvokeStructured_NonJSONResult_FailsPredictably(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("draft-3", "no json here, just prose")

	_, err := planningagent.InvokeStructured[draftRequest, draftResult](
		context.Background(), backend, "draft-3", draftRequest{Topic: "forge"}, buildDraftPrompt, nil,
	)
	if err == nil {
		t.Fatal("InvokeStructured: want error for missing structured result, got nil")
	}
}

func TestInvokeStructured_MalformedJSON_FailsPredictably(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("draft-4", "{not valid json")

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

func TestInvokeStructured_RetriesTransientBackendErrorThenSucceeds(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramError("draft-8", errors.New("transient boom"))
	backend.ProgramResult("draft-8", "{\"title\":\"Recovered\",\"body\":\"ok\"}")

	res, err := planningagent.InvokeStructured[draftRequest, draftResult](
		context.Background(), backend, "draft-8", draftRequest{Topic: "forge"}, buildDraftPrompt, nil,
	)
	if err != nil {
		t.Fatalf("InvokeStructured: %v", err)
	}
	if res.Title != "Recovered" {
		t.Errorf("res.Title = %q, want Recovered", res.Title)
	}

	invocations := backend.Invocations()
	if len(invocations) != 2 {
		t.Fatalf("Invocations() len = %d, want 2 (one failed, one succeeded)", len(invocations))
	}
	if invocations[0].Prompt != invocations[1].Prompt {
		t.Errorf("retry re-issued a different prompt: %q vs %q", invocations[0].Prompt, invocations[1].Prompt)
	}
}

func TestInvokeStructured_RetriesStrictDecodeFailureThenSucceeds(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("draft-9", "not json at all, no fenced block either")
	backend.ProgramResult("draft-9", "{\"title\":\"Recovered\",\"body\":\"ok\"}")

	res, err := planningagent.InvokeStructured[draftRequest, draftResult](
		context.Background(), backend, "draft-9", draftRequest{Topic: "forge"}, buildDraftPrompt, nil,
	)
	if err != nil {
		t.Fatalf("InvokeStructured: %v", err)
	}
	if res.Title != "Recovered" {
		t.Errorf("res.Title = %q, want Recovered", res.Title)
	}
	if len(backend.Invocations()) != 2 {
		t.Fatalf("Invocations() len = %d, want 2", len(backend.Invocations()))
	}
}

func TestInvokeStructured_RetriesValidateFailureThenSucceeds(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramResult("draft-10", "{\"title\":\"\",\"body\":\"missing title\"}")
	backend.ProgramResult("draft-10", "{\"title\":\"Has Title\",\"body\":\"ok\"}")

	validate := func(r draftResult) error {
		if r.Title == "" {
			return errors.New("title required")
		}
		return nil
	}

	res, err := planningagent.InvokeStructured[draftRequest, draftResult](
		context.Background(), backend, "draft-10", draftRequest{Topic: "forge"}, buildDraftPrompt, validate,
	)
	if err != nil {
		t.Fatalf("InvokeStructured: %v", err)
	}
	if res.Title != "Has Title" {
		t.Errorf("res.Title = %q, want Has Title", res.Title)
	}
	if len(backend.Invocations()) != 2 {
		t.Fatalf("Invocations() len = %d, want 2", len(backend.Invocations()))
	}
}

func TestInvokeStructured_ExhaustsRetries_ReturnsDescriptiveError(t *testing.T) {
	backend := planningagent.NewFakeBackend()
	backend.ProgramError("draft-11", errors.New("always boom"))

	_, err := planningagent.InvokeStructured[draftRequest, draftResult](
		context.Background(), backend, "draft-11", draftRequest{Topic: "forge"}, buildDraftPrompt, nil,
	)
	if err == nil {
		t.Fatal("InvokeStructured: want error after exhausting retries, got nil")
	}
	if !strings.Contains(err.Error(), "draft-11") {
		t.Errorf("error %q does not name the invocation key draft-11", err.Error())
	}

	invocations := backend.Invocations()
	if len(invocations) < 2 {
		t.Fatalf("Invocations() len = %d, want a bounded retry of more than 1 attempt", len(invocations))
	}
	for _, want := range invocations {
		if want.Prompt != invocations[0].Prompt {
			t.Errorf("retry re-issued a different prompt: %q vs %q", invocations[0].Prompt, want.Prompt)
		}
	}
}

func TestInvokeStructured_NilBuild_DoesNotConsumeRetries(t *testing.T) {
	backend := planningagent.NewFakeBackend()

	_, err := planningagent.InvokeStructured[draftRequest, draftResult](
		context.Background(), backend, "draft-12", draftRequest{Topic: "forge"}, nil, nil,
	)
	if err == nil {
		t.Fatal("InvokeStructured: want error for nil build, got nil")
	}
	if len(backend.Invocations()) != 0 {
		t.Errorf("Invocations() len = %d, want 0 (structural error must not invoke the backend)", len(backend.Invocations()))
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
	backend.ProgramResult("draft-7", `{"title":"t","body":"b"}`)

	raw, err := backend.Invoke(context.Background(), planningagent.InvokeRequest{
		Key:    "draft-7",
		Prompt: "draft something",
		Schema: []byte(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if raw != `{"title":"t","body":"b"}` {
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
	backend.ProgramDefault(`{"title":"default","body":"d"}`)
	backend.ProgramResult("scripted", `{"title":"first","body":"a"}`)
	backend.ProgramResult("scripted", `{"title":"second","body":"b"}`)

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

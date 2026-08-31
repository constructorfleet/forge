package planningagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// maxInvokeAttempts bounds InvokeStructured's retry of transient failures.
// Planning invocations are pure and idempotent, so re-issuing the same
// prompt on a transient failure is safe; this stays small because retries
// are not a substitute for surfacing a persistent failure to the caller.
const maxInvokeAttempts = 3

// InvokeStructured builds a prompt from req via build, invokes backend
// (identified for scripting/diagnostics by key), and strictly decodes the
// backend's raw output into the typed Res. A result that doesn't decode
// into Res, or that fails validate (if non-nil), fails predictably with a
// descriptive error rather than returning a zero Res disguised as success.
//
// A backend invocation error, a strict-decode failure, or a validate
// failure is treated as transient and retried up to maxInvokeAttempts times
// with the same prompt; a structural error (e.g. a nil build) returns
// immediately without consuming a retry. On exhaustion, InvokeStructured
// returns a single descriptive error rather than a zero Res disguised as
// success.
func InvokeStructured[Req any, Res any](
	ctx context.Context,
	backend Backend,
	key string,
	req Req,
	build func(Req) string,
	validate func(Res) error,
) (Res, error) {
	var zero Res
	if build == nil {
		return zero, fmt.Errorf("planningagent: build must not be nil")
	}

	schema, err := schemaFor[Res]()
	if err != nil {
		return zero, fmt.Errorf("planningagent: derive schema: %w", err)
	}

	prompt := build(req)

	var lastErr error
	for attempt := 1; attempt <= maxInvokeAttempts; attempt++ {
		res, err := invokeOnce[Res](ctx, backend, key, prompt, schema, validate)
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	return zero, fmt.Errorf("planningagent: invocation %q failed after %d attempts: %w", key, maxInvokeAttempts, lastErr)
}

// invokeOnce performs a single invoke->decode->validate cycle for
// InvokeStructured, returning an error for any of: a backend invocation
// error, a strict-decode failure, or a validate failure.
func invokeOnce[Res any](
	ctx context.Context,
	backend Backend,
	key string,
	prompt string,
	schema []byte,
	validate func(Res) error,
) (Res, error) {
	var zero Res
	raw, err := backend.Invoke(ctx, InvokeRequest{Key: key, Prompt: prompt, Schema: schema})
	if err != nil {
		return zero, fmt.Errorf("invoke: %w", err)
	}

	res, err := decodeStrict[Res](raw)
	if err != nil {
		return zero, fmt.Errorf("planningagent: no valid structured result found in backend output: %w", err)
	}
	if validate != nil {
		if err := validate(res); err != nil {
			return zero, fmt.Errorf("planningagent: no valid structured result found in backend output: %w", err)
		}
	}
	return res, nil
}

// schemaFor derives the JSON Schema for Res via jsonschema-go's reflection-
// based inference, marshaled to bytes for InvokeRequest.Schema.
func schemaFor[Res any]() ([]byte, error) {
	schema, err := jsonschema.For[Res](nil)
	if err != nil {
		return nil, err
	}
	return json.Marshal(schema)
}

// decodeStrict decodes raw directly into Res, rejecting unknown fields. It
// is InvokeStructured's only decode path -- backends are expected to return
// a bare JSON result matching the schema threaded through
// InvokeRequest.Schema.
func decodeStrict[Res any](raw string) (Res, error) {
	var res Res
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&res); err != nil {
		var zero Res
		return zero, err
	}
	return res, nil
}

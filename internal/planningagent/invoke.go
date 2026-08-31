package planningagent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// fencedJSONBlock matches fenced code blocks (optionally tagged "json") so
// InvokeStructured can pull a structured result out of otherwise free-form
// backend output. The closing fence is anchored to the start of a line
// ((?m:^```)) so a result whose content contains a literal ``` sequence
// mid-line doesn't get mistaken for the block's end. Mirrors
// internal/agent/claude's parseStructuredResult, generalized over the
// caller's typed result rather than Phase 1's fixed status shape.
var fencedJSONBlock = regexp.MustCompile("(?s)```(?:json)?[ \t]*\r?\n(.*?)\r?\n(?m:^```)")

// InvokeStructured builds a prompt from req via build, invokes backend
// (identified for scripting/diagnostics by key), and extracts, decodes, and
// validates a fenced-JSON result from the backend's raw output into the
// typed Res. It scans fenced blocks from last to first -- matching the
// convention that a backend's authoritative result is the final well-formed
// block it emits -- and returns the first one (scanning backward) that both
// decodes into Res and, if validate is non-nil, passes validate. An invalid
// or missing structured response fails predictably with a descriptive
// error rather than returning a zero Res disguised as success.
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
	raw, err := backend.Invoke(ctx, InvokeRequest{Key: key, Prompt: prompt, Schema: schema})
	if err != nil {
		return zero, fmt.Errorf("planningagent: invoke: %w", err)
	}

	if res, err := decodeStrict[Res](raw); err == nil {
		if validate == nil {
			return res, nil
		}
		if err := validate(res); err == nil {
			return res, nil
		}
	}

	res, ok := extractStructuredResult(raw, validate)
	if !ok {
		return zero, fmt.Errorf("planningagent: no valid structured result found in backend output")
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
// is InvokeStructured's primary decode path -- for backends that return a
// bare JSON result matching the schema threaded through InvokeRequest.Schema,
// rather than a fenced block buried in free-form prose.
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

// extractStructuredResult scans raw for fenced code blocks, from last to
// first, and returns the first one (in that scan order) that both decodes
// into Res and satisfies validate (if non-nil).
func extractStructuredResult[Res any](raw string, validate func(Res) error) (Res, bool) {
	var zero Res
	matches := fencedJSONBlock.FindAllStringSubmatch(raw, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		var res Res
		if err := json.Unmarshal([]byte(matches[i][1]), &res); err != nil {
			continue
		}
		if validate != nil {
			if err := validate(res); err != nil {
				continue
			}
		}
		return res, true
	}
	return zero, false
}

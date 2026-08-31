package claude

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// TestBuildResultJSONSchema_RoundTripsRealValues is the red test for issue
// 194: resultJSONSchema must be derived from structuredResult (via
// jsonschema-go) rather than hand-maintained, but still accept every shape
// of structuredResult that Execute actually produces.
func TestBuildResultJSONSchema_RoundTripsRealValues(t *testing.T) {
	resolved := resolvedResultSchema(t)

	valid := []string{
		`{"status":"IMPLEMENTED","summary":"done"}`,
		`{"status":"FAILED","summary":"could not build"}`,
		`{"status":"NEEDS_INFO","summary":"blocked","needs_info":{"question":"which backend?"}}`,
		`{"status":"NEEDS_INFO","summary":"blocked","needs_info":{"question":"which backend?","context":"two candidates"}}`,
		`{"status":"IMPLEMENTED","summary":"done","usage":{"input_tokens":10,"output_tokens":5}}`,
	}
	for _, raw := range valid {
		var instance any
		if err := json.Unmarshal([]byte(raw), &instance); err != nil {
			t.Fatalf("test fixture is not valid JSON: %v", err)
		}
		if err := resolved.Validate(instance); err != nil {
			t.Errorf("Validate(%s) = %v, want nil", raw, err)
		}
	}
}

// TestBuildResultJSONSchema_RejectsInvalidShapes asserts the derived schema
// still rejects the shapes the hand-authored resultJSONSchema const
// rejected: an unrecognized status enum value, a missing required field,
// and an unexpected top-level property.
func TestBuildResultJSONSchema_RejectsInvalidShapes(t *testing.T) {
	resolved := resolvedResultSchema(t)

	invalid := []string{
		`{"status":"BOGUS","summary":"done"}`,
		`{"summary":"missing status"}`,
		`{"status":"IMPLEMENTED"}`,
		`{"status":"IMPLEMENTED","summary":"done","extra":"nope"}`,
	}
	for _, raw := range invalid {
		var instance any
		if err := json.Unmarshal([]byte(raw), &instance); err != nil {
			t.Fatalf("test fixture is not valid JSON: %v", err)
		}
		if err := resolved.Validate(instance); err == nil {
			t.Errorf("Validate(%s) = nil, want an error", raw)
		}
	}
}

// TestResultJSONSchema_MatchesEnforcedShape locks in the shape asserted by
// TestExecute_ArgsIncludeJSONSchema: an object type, status enum, string
// summary, and required:[status,summary].
func TestResultJSONSchema_MatchesEnforcedShape(t *testing.T) {
	var decoded struct {
		Type       string `json:"type"`
		Properties struct {
			Status struct {
				Type string   `json:"type"`
				Enum []string `json:"enum"`
			} `json:"status"`
			Summary struct {
				Type string `json:"type"`
			} `json:"summary"`
		} `json:"properties"`
		Required             []string `json:"required"`
		AdditionalProperties bool     `json:"additionalProperties"`
	}
	if err := json.Unmarshal([]byte(resultJSONSchema), &decoded); err != nil {
		t.Fatalf("resultJSONSchema is not valid JSON: %v", err)
	}
	if decoded.Type != "object" {
		t.Fatalf("type = %q, want object", decoded.Type)
	}
	wantEnum := []string{"IMPLEMENTED", "NEEDS_INFO", "FAILED"}
	if len(decoded.Properties.Status.Enum) != len(wantEnum) {
		t.Fatalf("status enum = %v, want %v", decoded.Properties.Status.Enum, wantEnum)
	}
	for i, v := range wantEnum {
		if decoded.Properties.Status.Enum[i] != v {
			t.Fatalf("status enum = %v, want %v", decoded.Properties.Status.Enum, wantEnum)
		}
	}
	if decoded.Properties.Summary.Type != "string" {
		t.Fatalf("summary type = %q, want string", decoded.Properties.Summary.Type)
	}
	if len(decoded.Required) != 2 || decoded.Required[0] != "status" || decoded.Required[1] != "summary" {
		t.Fatalf("required = %v, want [status summary]", decoded.Required)
	}
	if decoded.AdditionalProperties {
		t.Fatalf("additionalProperties = true, want false")
	}
}

// resolvedResultSchema derives and resolves the structuredResult JSON schema
// for use with jsonschema.Resolved.Validate in these tests.
func resolvedResultSchema(t *testing.T) *jsonschema.Resolved {
	t.Helper()
	schema, err := buildResultJSONSchema()
	if err != nil {
		t.Fatalf("buildResultJSONSchema() error: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("schema.Resolve() error: %v", err)
	}
	return resolved
}

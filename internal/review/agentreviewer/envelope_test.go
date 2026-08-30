package agentreviewer

import "testing"

func TestParseEnvelope_ParsesCleanJSON(t *testing.T) {
	env, err := parseEnvelope(`{"axis":"bugs","findings":[{"severity":"HIGH","confidence":0.8,"file":"a.go","line":1,"message":"m","evidence":"e","remedy":"r"}]}`)
	if err != nil {
		t.Fatalf("parseEnvelope() error = %v", err)
	}
	if env.Axis != "bugs" {
		t.Errorf("Axis = %q, want %q", env.Axis, "bugs")
	}
	if len(env.Findings) != 1 {
		t.Fatalf("Findings = %+v, want 1", env.Findings)
	}
	f := env.Findings[0]
	if f.Severity != "HIGH" || f.Confidence != 0.8 || f.File != "a.go" || f.Line != 1 || f.Message != "m" || f.Evidence != "e" || f.Remedy != "r" {
		t.Errorf("finding = %+v, unexpected", f)
	}
}

func TestParseEnvelope_TolerantOfMarkdownCodeFence(t *testing.T) {
	raw := "```json\n{\"axis\":\"bugs\",\"findings\":[]}\n```"
	env, err := parseEnvelope(raw)
	if err != nil {
		t.Fatalf("parseEnvelope() error = %v", err)
	}
	if env.Axis != "bugs" || len(env.Findings) != 0 {
		t.Errorf("env = %+v, want empty bugs envelope", env)
	}
}

func TestParseEnvelope_TolerantOfSurroundingProse(t *testing.T) {
	raw := "Here is my review:\n{\"axis\":\"bugs\",\"findings\":[]}\nDone."
	env, err := parseEnvelope(raw)
	if err != nil {
		t.Fatalf("parseEnvelope() error = %v", err)
	}
	if env.Axis != "bugs" {
		t.Errorf("Axis = %q, want %q", env.Axis, "bugs")
	}
}

func TestParseEnvelope_EmptyInput_Errors(t *testing.T) {
	if _, err := parseEnvelope(""); err == nil {
		t.Fatal("parseEnvelope(\"\") error = nil, want error")
	}
}

func TestParseEnvelope_NoJSONObject_Errors(t *testing.T) {
	if _, err := parseEnvelope("nothing but plain text"); err == nil {
		t.Fatal("parseEnvelope() error = nil, want error")
	}
}

func TestParseEnvelope_InvalidJSON_Errors(t *testing.T) {
	if _, err := parseEnvelope(`{"axis": "bugs", "findings": [`); err == nil {
		t.Fatal("parseEnvelope() error = nil, want error for truncated/invalid JSON")
	}
}

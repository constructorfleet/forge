package clicommon

import "testing"

func TestParseStructuredResult_FindsLastWellFormedBlock(t *testing.T) {
	text := "some preamble\n" +
		"```json\n{\"status\":\"FAILED\",\"summary\":\"ignored, not last\"}\n```\n" +
		"more text\n" +
		"```json\n{\"status\":\"IMPLEMENTED\",\"summary\":\"done\"}\n```\n"

	res, ok := ParseStructuredResult(text)
	if !ok {
		t.Fatalf("ParseStructuredResult: ok = false, want true")
	}
	if res.Status != "IMPLEMENTED" || res.Summary != "done" {
		t.Errorf("res = %+v, want status IMPLEMENTED summary done", res)
	}
}

func TestParseStructuredResult_NoFencedBlockReturnsNotOK(t *testing.T) {
	_, ok := ParseStructuredResult("just plain text, no fenced block")
	if ok {
		t.Fatalf("ParseStructuredResult: ok = true, want false")
	}
}

func TestParseStructuredResult_RepairsTrailingComma(t *testing.T) {
	text := "```json\n{\"status\":\"IMPLEMENTED\",\"summary\":\"done\",}\n```\n"
	res, ok := ParseStructuredResult(text)
	if !ok {
		t.Fatalf("ParseStructuredResult: ok = false, want true")
	}
	if res.Status != "IMPLEMENTED" {
		t.Errorf("res.Status = %q, want IMPLEMENTED", res.Status)
	}
}

func TestParseStructuredResult_UnrecognizedStatusIsSkipped(t *testing.T) {
	text := "```json\n{\"status\":\"BOGUS\",\"summary\":\"nope\"}\n```\n"
	_, ok := ParseStructuredResult(text)
	if ok {
		t.Fatalf("ParseStructuredResult: ok = true, want false for unrecognized status")
	}
}

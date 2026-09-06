package initdiscovery

import (
	"reflect"
	"testing"
)

func TestProbeLanguageServers_PythonSelectsPylspWhenPyrightAbsent(t *testing.T) {
	withFakePATH(t, "pylsp")

	result := probeLanguageServers([]string{"Python"})

	if result.Enabled["Python"] != "pylsp" {
		t.Errorf("Enabled[\"Python\"] = %q, want %q", result.Enabled["Python"], "pylsp")
	}
	if len(result.Missing) != 0 {
		t.Errorf("Missing = %v, want empty", result.Missing)
	}
}

func TestProbeLanguageServers_PythonMissingWhenBothCandidatesAbsent(t *testing.T) {
	withFakePATH(t)

	result := probeLanguageServers([]string{"Python"})

	if _, ok := result.Enabled["Python"]; ok {
		t.Errorf("Enabled[\"Python\"] = %q, want absent (neither candidate on PATH)", result.Enabled["Python"])
	}
	if !reflect.DeepEqual(result.Missing, []string{"Python"}) {
		t.Errorf("Missing = %v, want [Python]", result.Missing)
	}
}

func TestProbeLanguageServers_AllCandidatesMissing_NoCrashNoEnablement(t *testing.T) {
	withFakePATH(t)

	result := probeLanguageServers([]string{"Go", "Rust", "Ruby"})

	if len(result.Enabled) != 0 {
		t.Errorf("Enabled = %v, want empty", result.Enabled)
	}
	if !reflect.DeepEqual(result.Missing, []string{"Go", "Rust", "Ruby"}) {
		t.Errorf("Missing = %v, want [Go Rust Ruby]", result.Missing)
	}
}

func TestProbeLanguageServers_UnknownLanguageIgnored(t *testing.T) {
	withFakePATH(t)

	result := probeLanguageServers([]string{"COBOL"})

	if len(result.Enabled) != 0 || len(result.Missing) != 0 {
		t.Errorf("probeLanguageServers(COBOL) = %+v, want empty result", result)
	}
}

func TestDetect_LSPProbe_RecordsEnabledBinaryForDetectedLanguage(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	withFakePATH(t, "gopls")
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.25\n")

	result := Detect(dir)

	if result.LSPProbe.Enabled["Go"] != "gopls" {
		t.Errorf("LSPProbe.Enabled[\"Go\"] = %q, want %q", result.LSPProbe.Enabled["Go"], "gopls")
	}
}

func TestDetect_LSPProbe_RecordsMissingLanguageWithNoBinaryOnPATH(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	withFakePATH(t)
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.25\n")

	result := Detect(dir)

	found := false
	for _, language := range result.LSPProbe.Missing {
		if language == "Go" {
			found = true
		}
	}
	if !found {
		t.Errorf("LSPProbe.Missing = %v, want to contain Go", result.LSPProbe.Missing)
	}
}

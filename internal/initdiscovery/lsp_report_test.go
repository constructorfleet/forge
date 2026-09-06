package initdiscovery

import "testing"

func TestFormatMissingBinariesReport_ListsEachMissingLanguageWithBinaryAndHint(t *testing.T) {
	missing := []MissingBinary{
		{Language: "Rust", Binary: "rust-analyzer", InstallHint: "rustup component add rust-analyzer"},
		{Language: "Java", Binary: "jdtls", InstallHint: "brew install jdtls"},
	}

	lines := FormatMissingBinariesReport(missing)

	want := []string{
		"Rust: rust-analyzer not found on PATH. Install it: rustup component add rust-analyzer",
		"Java: jdtls not found on PATH. Install it: brew install jdtls",
	}
	if len(lines) != len(want) {
		t.Fatalf("FormatMissingBinariesReport lines = %v, want %v", lines, want)
	}
	for i, line := range lines {
		if line != want[i] {
			t.Errorf("line %d = %q, want %q", i, line, want[i])
		}
	}
}

func TestFormatMissingBinariesReport_EmptyInputProducesNoLines(t *testing.T) {
	lines := FormatMissingBinariesReport(nil)

	if len(lines) != 0 {
		t.Errorf("FormatMissingBinariesReport(nil) = %v, want empty", lines)
	}
}

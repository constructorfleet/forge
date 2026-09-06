package lsp

import (
	"testing"

	"github.com/Teagan42/forge/internal/config"
)

// TestLanguages_ExactSet checks that the table names exactly the seven
// launch languages, with no other language present.
func TestLanguages_ExactSet(t *testing.T) {
	want := []string{
		"Go",
		"TypeScript/JavaScript",
		"Python",
		"Rust",
		"C/C++",
		"Java",
		"Ruby",
	}

	if len(Languages) != len(want) {
		t.Fatalf("got %d languages, want %d: %+v", len(Languages), len(want), Languages)
	}
	for i, spec := range Languages {
		if spec.Language != want[i] {
			t.Errorf("language[%d] = %q, want %q", i, spec.Language, want[i])
		}
	}
}

// TestLanguages_CandidateOrder checks that Python and Ruby preserve their
// documented candidate binary order: Python tries pyright before pylsp,
// and Ruby tries solargraph before ruby-lsp.
func TestLanguages_CandidateOrder(t *testing.T) {
	cases := []struct {
		language string
		want     []string
	}{
		{"Python", []string{"pyright", "pylsp"}},
		{"Ruby", []string{"solargraph", "ruby-lsp"}},
	}

	for _, c := range cases {
		spec := findLanguageSpec(t, c.language)
		if len(spec.Binaries) != len(c.want) {
			t.Fatalf("%s: got %d candidate binaries, want %d: %+v", c.language, len(spec.Binaries), len(c.want), spec.Binaries)
		}
		for i, binary := range c.want {
			if spec.Binaries[i].Name != binary {
				t.Errorf("%s: candidate[%d] = %q, want %q", c.language, i, spec.Binaries[i].Name, binary)
			}
		}
	}
}

// TestLanguages_InstallHintsNonEmpty checks that every candidate binary in
// the table carries a non-empty static install hint.
func TestLanguages_InstallHintsNonEmpty(t *testing.T) {
	for _, spec := range Languages {
		for _, binary := range spec.Binaries {
			if binary.InstallHint == "" {
				t.Errorf("%s: binary %q has an empty install hint", spec.Language, binary.Name)
			}
		}
	}
}

// TestLanguages_RegistryIDMatchesRegistry checks that each LanguageSpec's
// RegistryID names the exact key the live Registry uses for a language the
// registry already serves, so a caller of Detect never needs its own
// display-name-to-key bridge. It reads the real Registry (via NewRegistry,
// seeded from builtinServers) rather than a hand-written expectation table,
// so a rename of a registry key (for example "javascript" to "js") fails
// this test instead of passing silently.
func TestLanguages_RegistryIDMatchesRegistry(t *testing.T) {
	registry := NewRegistry(config.LSPConfig{})

	// registryServedLanguages names the languages Registry serves today
	// (see builtinServers). A language absent from this set must have no
	// matching Registry entry; a language present in it must have one.
	registryServedLanguages := map[string]bool{
		"Go":                    true,
		"TypeScript/JavaScript": true,
		"Python":                true,
		"Rust":                  true,
	}

	for _, spec := range Languages {
		_, servedByRegistry := registry[spec.RegistryID]
		switch {
		case registryServedLanguages[spec.Language] && !servedByRegistry:
			t.Errorf("%s: RegistryID %q has no matching entry in the live Registry", spec.Language, spec.RegistryID)
		case !registryServedLanguages[spec.Language] && servedByRegistry:
			t.Errorf("%s: RegistryID %q unexpectedly matches a live Registry entry; update registryServedLanguages", spec.Language, spec.RegistryID)
		}
	}
}

// TestLanguageID_ReturnsRegistryIDForKnownDisplayName checks that LanguageID
// resolves a Language-to-LSP Table display name to its RegistryID.
func TestLanguageID_ReturnsRegistryIDForKnownDisplayName(t *testing.T) {
	if got := LanguageID("TypeScript/JavaScript"); got != "javascript" {
		t.Errorf("LanguageID(%q) = %q, want %q", "TypeScript/JavaScript", got, "javascript")
	}
}

// TestLanguageID_FallsBackToLowercaseForUnknownDisplayName checks that
// LanguageID falls back to a plain lowercase of a name absent from the
// Language-to-LSP Table, since no registry entry can match it either way.
func TestLanguageID_FallsBackToLowercaseForUnknownDisplayName(t *testing.T) {
	if got := LanguageID("COBOL"); got != "cobol" {
		t.Errorf("LanguageID(%q) = %q, want %q", "COBOL", got, "cobol")
	}
}

func findLanguageSpec(t *testing.T, language string) LanguageSpec {
	t.Helper()
	for _, spec := range Languages {
		if spec.Language == language {
			return spec
		}
	}
	t.Fatalf("no LanguageSpec found for language %q", language)
	return LanguageSpec{}
}

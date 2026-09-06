package lsp

import "testing"

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

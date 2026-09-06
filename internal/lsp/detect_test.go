package lsp_test

import (
	"reflect"
	"testing"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/lsp"
	"github.com/Teagan42/forge/internal/semantic/lspdriver"
)

func TestNewRegistry_BuiltinGo(t *testing.T) {
	withFakePATH(t)

	registry := lsp.NewRegistry(config.LSPConfig{})

	spec, ok := registry["go"]
	if !ok {
		t.Fatal(`registry["go"] missing, want built-in gopls entry`)
	}
	if !reflect.DeepEqual(spec.Command, []string{"gopls"}) {
		t.Errorf(`registry["go"].Command = %v, want ["gopls"]`, spec.Command)
	}
}

func TestNewRegistry_ConfigOverridesBuiltin(t *testing.T) {
	withFakePATH(t)

	registry := lsp.NewRegistry(config.LSPConfig{
		Servers: map[string]config.LSPServerConfig{
			"go": {Command: []string{"custom-gopls", "--flag"}},
		},
	})

	spec := registry["go"]
	if !reflect.DeepEqual(spec.Command, []string{"custom-gopls", "--flag"}) {
		t.Errorf(`registry["go"].Command = %v, want ["custom-gopls" "--flag"]`, spec.Command)
	}
}

func TestNewRegistry_ConfigOverridePreservesBuiltinProfile(t *testing.T) {
	withFakePATH(t) // neither pyright nor pylsp present: falls back to pyright's profile

	registry := lsp.NewRegistry(config.LSPConfig{
		Servers: map[string]config.LSPServerConfig{
			"python": {Command: []string{"custom-pyright", "--stdio"}},
		},
	})

	spec := registry["python"]
	if !reflect.DeepEqual(spec.Command, []string{"custom-pyright", "--stdio"}) {
		t.Errorf(`registry["python"].Command = %v, want ["custom-pyright" "--stdio"]`, spec.Command)
	}
	if spec.Profile.HoverStyle != lspdriver.HoverStylePyrightAnnotated {
		t.Errorf("registry[\"python\"].Profile.HoverStyle = %v, want HoverStylePyrightAnnotated", spec.Profile.HoverStyle)
	}
	if !spec.Profile.DropSymbolChildren {
		t.Error(`registry["python"].Profile.DropSymbolChildren = false, want true (built-in profile must survive command override)`)
	}
}

func TestNewRegistry_ConfigOnlyLanguageGetsZeroProfile(t *testing.T) {
	withFakePATH(t)

	registry := lsp.NewRegistry(config.LSPConfig{
		Servers: map[string]config.LSPServerConfig{
			"kotlin": {Command: []string{"kotlin-language-server"}},
		},
	})

	spec, ok := registry["kotlin"]
	if !ok {
		t.Fatal(`registry["kotlin"] missing, want config-added entry`)
	}
	if !reflect.DeepEqual(spec.Profile, lspdriver.ServerProfile{}) {
		t.Errorf("registry[\"kotlin\"].Profile = %+v, want zero value", spec.Profile)
	}
}

// TestNewRegistry_BuiltinProfiles checks that with none of the candidate
// binaries on PATH, NewRegistry falls back to each language's first
// candidate — reproducing Forge's original v1 defaults exactly.
func TestNewRegistry_BuiltinProfiles(t *testing.T) {
	withFakePATH(t)

	registry := lsp.NewRegistry(config.LSPConfig{})

	cases := []struct {
		language string
		command  []string
		profile  lspdriver.ServerProfile
	}{
		{"go", []string{"gopls"}, lspdriver.ServerProfile{HoverStyle: lspdriver.HoverStyleFirstFence}},
		{"rust", []string{"rust-analyzer"}, lspdriver.ServerProfile{HoverStyle: lspdriver.HoverStyleRustTwoFence}},
		{
			"python",
			[]string{"pyright-langserver", "--stdio"},
			lspdriver.ServerProfile{HoverStyle: lspdriver.HoverStylePyrightAnnotated, DropSymbolChildren: true},
		},
		{
			"javascript",
			[]string{"typescript-language-server", "--stdio"},
			lspdriver.ServerProfile{HoverStyle: lspdriver.HoverStyleFirstFence},
		},
	}

	for _, tc := range cases {
		spec, ok := registry[tc.language]
		if !ok {
			t.Errorf("registry[%q] missing, want built-in entry", tc.language)
			continue
		}
		if !reflect.DeepEqual(spec.Command, tc.command) {
			t.Errorf("registry[%q].Command = %v, want %v", tc.language, spec.Command, tc.command)
		}
		if !reflect.DeepEqual(spec.Profile, tc.profile) {
			t.Errorf("registry[%q].Profile = %+v, want %+v", tc.language, spec.Profile, tc.profile)
		}
	}
}

// TestNewRegistry_SelectsSecondCandidateWhenFirstAbsentFromPATH is the core
// reconciliation regression: when only Python's fallback candidate (pylsp)
// is on PATH, NewRegistry's python entry must launch pylsp, not silently
// keep pointing at pyright-langserver.
func TestNewRegistry_SelectsSecondCandidateWhenFirstAbsentFromPATH(t *testing.T) {
	withFakePATH(t, "pylsp")

	registry := lsp.NewRegistry(config.LSPConfig{})

	spec, ok := registry["python"]
	if !ok {
		t.Fatal(`registry["python"] missing`)
	}
	if !reflect.DeepEqual(spec.Command, []string{"pylsp"}) {
		t.Errorf(`registry["python"].Command = %v, want ["pylsp"] (only candidate on PATH)`, spec.Command)
	}
	if !reflect.DeepEqual(spec.Profile, lspdriver.ServerProfile{}) {
		t.Errorf("registry[\"python\"].Profile = %+v, want zero value (pylsp has no known hover quirk)", spec.Profile)
	}
}

// TestNewRegistry_CoversLanguagesWithoutV1BuiltinEntries checks that the
// three languages lsp.Languages added beyond Forge's original v1 registry
// (C/C++, Java, Ruby) now get a registry entry too, reconciling the two
// tables per the deferral in lsp/languages.go.
func TestNewRegistry_CoversLanguagesWithoutV1BuiltinEntries(t *testing.T) {
	withFakePATH(t)

	registry := lsp.NewRegistry(config.LSPConfig{})

	cases := []struct {
		key     string
		command []string
	}{
		{"cpp", []string{"clangd"}},
		{"java", []string{"jdtls"}},
		{"ruby", []string{"solargraph"}},
	}
	for _, tc := range cases {
		spec, ok := registry[tc.key]
		if !ok {
			t.Errorf("registry[%q] missing, want an entry reconciled from lsp.Languages", tc.key)
			continue
		}
		if !reflect.DeepEqual(spec.Command, tc.command) {
			t.Errorf("registry[%q].Command = %v, want %v", tc.key, spec.Command, tc.command)
		}
	}
}

func TestNewRegistry_ConfigExtendsWithNewLanguage(t *testing.T) {
	withFakePATH(t)

	registry := lsp.NewRegistry(config.LSPConfig{
		Servers: map[string]config.LSPServerConfig{
			"rust": {Command: []string{"rust-analyzer"}},
		},
	})

	if _, ok := registry["go"]; !ok {
		t.Error(`registry["go"] missing, want built-in entry to survive extension`)
	}
	spec, ok := registry["rust"]
	if !ok {
		t.Fatal(`registry["rust"] missing, want config-added entry`)
	}
	if !reflect.DeepEqual(spec.Command, []string{"rust-analyzer"}) {
		t.Errorf(`registry["rust"].Command = %v, want ["rust-analyzer"]`, spec.Command)
	}
}

func TestExtensions_BuiltinTable(t *testing.T) {
	got := lsp.Extensions(config.LSPConfig{})

	want := map[string]string{
		".go":  "go",
		".rs":  "rust",
		".py":  "python",
		".js":  "javascript",
		".jsx": "javascript",
		".ts":  "javascript",
		".tsx": "javascript",
		".mjs": "javascript",
		".cjs": "javascript",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Extensions() = %v, want %v", got, want)
	}
}

func TestExtensions_ConfigOverridesAndExtends(t *testing.T) {
	got := lsp.Extensions(config.LSPConfig{
		Extensions: map[string]string{
			".rs": "go",     // operator repoints an existing extension
			".kt": "kotlin", // and adds an unknown one
		},
	})

	if got[".rs"] != "go" {
		t.Errorf("Extensions()[\".rs\"] = %q, want %q (config overrides the built-in)", got[".rs"], "go")
	}
	if got[".kt"] != "kotlin" {
		t.Errorf("Extensions()[\".kt\"] = %q, want %q (config extends the table)", got[".kt"], "kotlin")
	}
	if got[".go"] != "go" {
		t.Errorf("Extensions()[\".go\"] = %q, want the built-in row to survive extension", got[".go"])
	}
}

func TestDetect_GoRepoYieldsGopls(t *testing.T) {
	withFakePATH(t)
	registry := lsp.NewRegistry(config.LSPConfig{})

	got := lsp.Detect([]string{"Go"}, registry)

	want := []lsp.DetectedServer{{
		Language: "go",
		Command:  []string{"gopls"},
		Profile:  lspdriver.ServerProfile{HoverStyle: lspdriver.HoverStyleFirstFence},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Detect() = %+v, want %+v", got, want)
	}
}

func TestDetect_UnregisteredLanguageYieldsNone(t *testing.T) {
	withFakePATH(t)
	registry := lsp.NewRegistry(config.LSPConfig{})

	got := lsp.Detect([]string{"Kotlin"}, registry)

	if len(got) != 0 {
		t.Errorf("Detect() = %+v, want empty", got)
	}
}

func TestDetect_SevenLanguagesYieldSevenServers(t *testing.T) {
	withFakePATH(t)
	registry := lsp.NewRegistry(config.LSPConfig{})

	got := lsp.Detect([]string{"Go", "Python", "Rust", "JavaScript", "Cpp", "Java", "Ruby"}, registry)

	want := []lsp.DetectedServer{
		{
			Language: "go",
			Command:  []string{"gopls"},
			Profile:  lspdriver.ServerProfile{HoverStyle: lspdriver.HoverStyleFirstFence},
		},
		{
			Language: "python",
			Command:  []string{"pyright-langserver", "--stdio"},
			Profile: lspdriver.ServerProfile{
				HoverStyle:         lspdriver.HoverStylePyrightAnnotated,
				DropSymbolChildren: true,
			},
		},
		{
			Language: "rust",
			Command:  []string{"rust-analyzer"},
			Profile:  lspdriver.ServerProfile{HoverStyle: lspdriver.HoverStyleRustTwoFence},
		},
		{
			Language: "javascript",
			Command:  []string{"typescript-language-server", "--stdio"},
			Profile:  lspdriver.ServerProfile{HoverStyle: lspdriver.HoverStyleFirstFence},
		},
		{Language: "cpp", Command: []string{"clangd"}},
		{Language: "java", Command: []string{"jdtls"}},
		{Language: "ruby", Command: []string{"solargraph"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Detect() = %+v, want %+v", got, want)
	}
}

func TestDetect_ConfigForUndetectedLanguageProducesNoServer(t *testing.T) {
	withFakePATH(t)
	registry := lsp.NewRegistry(config.LSPConfig{
		Servers: map[string]config.LSPServerConfig{
			"kotlin": {Command: []string{"kotlin-language-server"}},
		},
	})

	got := lsp.Detect([]string{"Go"}, registry)

	want := []lsp.DetectedServer{{
		Language: "go",
		Command:  []string{"gopls"},
		Profile:  lspdriver.ServerProfile{HoverStyle: lspdriver.HoverStyleFirstFence},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Detect() = %+v, want %+v (kotlin configured but not detected must not appear)", got, want)
	}
}

func TestDetect_NoLanguages(t *testing.T) {
	withFakePATH(t)
	registry := lsp.NewRegistry(config.LSPConfig{})

	got := lsp.Detect(nil, registry)

	if len(got) != 0 {
		t.Errorf("Detect() = %+v, want empty", got)
	}
}

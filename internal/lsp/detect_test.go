package lsp_test

import (
	"reflect"
	"testing"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/lsp"
)

func TestNewRegistry_BuiltinGo(t *testing.T) {
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

func TestNewRegistry_ConfigExtendsWithNewLanguage(t *testing.T) {
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

func TestDetect_GoRepoYieldsGopls(t *testing.T) {
	registry := lsp.NewRegistry(config.LSPConfig{})

	got := lsp.Detect([]string{"Go"}, registry)

	want := []lsp.DetectedServer{{Language: "go", Command: []string{"gopls"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Detect() = %+v, want %+v", got, want)
	}
}

func TestDetect_NonGoRepoYieldsNone(t *testing.T) {
	registry := lsp.NewRegistry(config.LSPConfig{})

	got := lsp.Detect([]string{"JavaScript", "Python"}, registry)

	if len(got) != 0 {
		t.Errorf("Detect() = %+v, want empty", got)
	}
}

func TestDetect_ConfigForUndetectedLanguageProducesNoServer(t *testing.T) {
	registry := lsp.NewRegistry(config.LSPConfig{
		Servers: map[string]config.LSPServerConfig{
			"rust": {Command: []string{"rust-analyzer"}},
		},
	})

	got := lsp.Detect([]string{"Go"}, registry)

	want := []lsp.DetectedServer{{Language: "go", Command: []string{"gopls"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Detect() = %+v, want %+v (rust configured but not detected must not appear)", got, want)
	}
}

func TestDetect_NoLanguages(t *testing.T) {
	registry := lsp.NewRegistry(config.LSPConfig{})

	got := lsp.Detect(nil, registry)

	if len(got) != 0 {
		t.Errorf("Detect() = %+v, want empty", got)
	}
}

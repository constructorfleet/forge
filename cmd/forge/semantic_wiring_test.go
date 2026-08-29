package main

import (
	"testing"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/semantic"
)

func boolPtr(b bool) *bool { return &b }

func TestLSPToSemanticConfig_TranslatesEnabledAndProviders(t *testing.T) {
	lsp := config.LSPConfig{
		Enabled: true,
		Providers: map[string]config.LSPProviderPreference{
			"definition": config.LSPProviderForgeManaged,
			"hover":      config.LSPProviderOff,
		},
	}

	got := lspToSemanticConfig(lsp, "claude-code")

	if !got.Enabled {
		t.Error("Enabled = false, want true")
	}
	if got.Providers["definition"] != semantic.ProviderPreferenceForgeManaged {
		t.Errorf(`Providers["definition"] = %q, want %q`, got.Providers["definition"], semantic.ProviderPreferenceForgeManaged)
	}
	if got.Providers["hover"] != semantic.ProviderPreferenceOff {
		t.Errorf(`Providers["hover"] = %q, want %q`, got.Providers["hover"], semantic.ProviderPreferenceOff)
	}
}

func TestLSPToSemanticConfig_FlattensCapabilitiesToTheConfiguredBackend(t *testing.T) {
	lsp := config.LSPConfig{
		Capabilities: map[string]config.LSPCapabilityOverride{
			"claude-code": {Definition: boolPtr(false), Hover: boolPtr(true)},
			"codex":       {Definition: boolPtr(true)},
		},
	}

	got := lspToSemanticConfig(lsp, "claude-code")

	if got.Override.Definition == nil || *got.Override.Definition != false {
		t.Errorf("Override.Definition = %v, want pointer to false (claude-code's override)", got.Override.Definition)
	}
	if got.Override.Hover == nil || *got.Override.Hover != true {
		t.Errorf("Override.Hover = %v, want pointer to true", got.Override.Hover)
	}
	if got.Override.References != nil {
		t.Errorf("Override.References = %v, want nil (unset by claude-code's override)", got.Override.References)
	}
}

func TestLSPToSemanticConfig_NoOverrideForConfiguredBackend_YieldsZeroOverride(t *testing.T) {
	lsp := config.LSPConfig{
		Capabilities: map[string]config.LSPCapabilityOverride{
			"codex": {Definition: boolPtr(true)},
		},
	}

	got := lspToSemanticConfig(lsp, "claude-code")

	if got.Override != (semantic.CapabilityOverride{}) {
		t.Errorf("Override = %+v, want zero value: no entry for claude-code", got.Override)
	}
}

func TestLSPToSemanticConfig_DefaultLSPConfig_YieldsDisabledConfig(t *testing.T) {
	got := lspToSemanticConfig(config.Default().LSP, config.Default().Agent.Provider)

	if got.Enabled {
		t.Error("Enabled = true, want false: config.Default() ships lsp disabled")
	}
}

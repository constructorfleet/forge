package domain_test

import (
	"testing"

	"github.com/Teagan42/forge/internal/domain"
)

func TestIssueScopeDistinguishesManagedAndExternal(t *testing.T) {
	if domain.ScopeManaged == domain.ScopeExternal {
		t.Fatalf("ScopeManaged and ScopeExternal must be distinct values")
	}

	managed := domain.Issue{Scope: domain.ScopeManaged}
	external := domain.Issue{Scope: domain.ScopeExternal}

	if !managed.IsManaged() {
		t.Fatalf("expected managed issue to report IsManaged() true")
	}
	if managed.IsExternal() {
		t.Fatalf("expected managed issue to report IsExternal() false")
	}

	if external.IsManaged() {
		t.Fatalf("expected external issue to report IsManaged() false")
	}
	if !external.IsExternal() {
		t.Fatalf("expected external issue to report IsExternal() true")
	}
}

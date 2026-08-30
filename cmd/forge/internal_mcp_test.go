package main

import "testing"

func TestRunInternalMCP_MissingWorkspaceFlag(t *testing.T) {
	code := runInternalMCP(nil)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (usage error)", code)
	}
}

func TestRunInternalMCP_UnknownFlag(t *testing.T) {
	code := runInternalMCP([]string{"--bogus"})
	if code != 2 {
		t.Fatalf("code = %d, want 2 (flag parse error)", code)
	}
}

func TestRunInternalMCP_NonexistentWorkspaceDir(t *testing.T) {
	code := runInternalMCP([]string{"--workspace", "/nonexistent/path/for/forge/internal-mcp/test"})
	if code != 1 {
		t.Fatalf("code = %d, want 1 (workspace does not exist)", code)
	}
}

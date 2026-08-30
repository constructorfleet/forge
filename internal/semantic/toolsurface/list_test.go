package toolsurface

import "testing"

func TestCapLocations_UnderLimitNotTruncated(t *testing.T) {
	items := []SourceLocation{{File: "a.go", Line: 1}, {File: "b.go", Line: 2}}

	got := capLocations(items, 5)

	if got.Truncated {
		t.Fatalf("Truncated = true, want false")
	}
	if got.Total != 2 {
		t.Fatalf("Total = %d, want 2", got.Total)
	}
	if len(got.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(got.Items))
	}
}

func TestCapLocations_OverLimitTruncated(t *testing.T) {
	items := make([]SourceLocation, 7)
	for i := range items {
		items[i] = SourceLocation{File: "a.go", Line: i + 1}
	}

	got := capLocations(items, 5)

	if !got.Truncated {
		t.Fatalf("Truncated = false, want true")
	}
	if got.Total != 7 {
		t.Fatalf("Total = %d, want 7", got.Total)
	}
	if len(got.Items) != 5 {
		t.Fatalf("len(Items) = %d, want 5", len(got.Items))
	}
	if got.Items[4].Line != 5 {
		t.Fatalf("Items[4].Line = %d, want 5 (first 5 kept in order)", got.Items[4].Line)
	}
}

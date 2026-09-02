package linear

import "testing"

func TestCapabilitiesReportsNativeDependencyLinks(t *testing.T) {
	c := NewClient(nil, "", "FOR")
	caps := c.Capabilities()
	if !caps.NativeDependencyLinks {
		t.Fatalf("Capabilities().NativeDependencyLinks = false, want true: Linear always exposes native issue relations")
	}
	if caps.PlanningMirror {
		t.Fatalf("Capabilities().PlanningMirror = true, want false")
	}
}

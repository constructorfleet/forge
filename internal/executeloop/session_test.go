package executeloop

import "testing"

func TestNewSession_StatusDefaultsToRunning(t *testing.T) {
	session := NewSession()

	if got := session.Status(); got != StatusRunning {
		t.Fatalf("Status() = %q, want %q", got, StatusRunning)
	}
}

func TestSession_SetStatus_TransitionsToNeedsInfo(t *testing.T) {
	session := NewSession()

	session.SetStatus(StatusNeedsInfo)

	if got := session.Status(); got != StatusNeedsInfo {
		t.Fatalf("Status() = %q, want %q", got, StatusNeedsInfo)
	}
}

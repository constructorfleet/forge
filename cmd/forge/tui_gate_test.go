package main

import "testing"

func TestShouldUseTUIExplicitFlagWins(t *testing.T) {
	if got := shouldUseTUI(false, true, true); got {
		t.Fatal("--no-tui on a TTY should disable the roster")
	}
	if got := shouldUseTUI(true, true, false); !got {
		t.Fatal("--tui off a TTY should force the roster on")
	}
}

func TestShouldUseTUIFallsBackToIsatty(t *testing.T) {
	if got := shouldUseTUI(false, false, true); !got {
		t.Fatal("terminal session with no flag should attach the roster")
	}
	if got := shouldUseTUI(false, false, false); got {
		t.Fatal("non-terminal session should stay silent")
	}
}

func TestTriStateTuiFlag(t *testing.T) {
	var ts triState
	if err := ts.tuiFlag("true"); err != nil {
		t.Fatalf("tuiFlag(true): %v", err)
	}
	val, set := ts.wasSet()
	if !set || !val {
		t.Fatalf("wasSet = %v,%v, want true,true", val, set)
	}
}

func TestTriStateNoTuiFlagInverts(t *testing.T) {
	var ts triState
	if err := ts.noTuiFlag("true"); err != nil {
		t.Fatalf("noTuiFlag(true): %v", err)
	}
	val, set := ts.wasSet()
	if !set || val {
		t.Fatalf("wasSet = %v,%v, want true,false", val, set)
	}
}

func TestTriStateBadValue(t *testing.T) {
	var ts triState
	if err := ts.tuiFlag("banana"); err == nil {
		t.Fatal("tuiFlag(banana): want error, got nil")
	}
}

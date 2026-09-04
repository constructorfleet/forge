package main

// tui_gate.go: whether `forge execute` attaches its live roster.

import (
	"strconv"

	"github.com/mattn/go-isatty"
)

// triState is a tri-state boolean flag (--tui / --no-tui): set only when the
// user names the flag, val carrying the resolved intent.
type triState struct {
	set bool
	val bool
}

// wasSet reports whether either flag was given and, if so, with what intent.
func (t *triState) wasSet() (val, set bool) { return t.val, t.set }

// tuiFlag binds --tui to the shared triState, recording its argument (bare
// --tui passes "true").
func (t *triState) tuiFlag(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	t.set, t.val = true, v
	return nil
}

// noTuiFlag binds --no-tui to the shared triState, inverting its argument so
// bare --no-tui reads as "off".
func (t *triState) noTuiFlag(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	t.set, t.val = true, !v
	return nil
}

// shouldUseTUI decides whether the live roster attaches. An explicit
// --tui/--no-tui flag wins over the isatty guess (both the flag and the guess
// are injected so the decision stays pure).
func shouldUseTUI(val, set, isTerminal bool) bool {
	if set {
		return val
	}
	return isTerminal
}

// isTerminalSession reports whether stdin and stdout are both interactive
// terminals, the default signal that a human is watching the run.
func isTerminalSession() bool {
	return isatty.IsTerminal(0) && isatty.IsTerminal(1)
}

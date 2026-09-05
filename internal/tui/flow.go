package tui

// flow.go factors the one control shape approve.go and answer.go both build:
// guard against a second key press, read a stored checkpoint off the update
// goroutine, defer it to a suspend-and-return process ($PAGER or $EDITOR),
// then fire the tracker/engine call and commit its result to the notice.
// actionFlow holds the two fields that shape repeated (an in-flight bool
// paired with the Issue the in-flight call names); guard/open/close/
// applyResult hold the surrounding behaviour every control repeated too. Each
// control keeps its own checkpoint type, render, and tracker/engine call: only
// the flow bookkeeping is shared.

import "fmt"

// actionFlow tracks one in-flight suspend-and-return control, from the
// artifact opening through the tracker/engine call returning.
type actionFlow struct {
	// inFlight records that a flow is running, so a second key press on the
	// same control cannot start a second one.
	inFlight bool
	// issueID is the Issue the in-flight flow names, read back once the
	// artifact process closes so the call fires against the row the
	// artifact was actually read for.
	issueID string
}

// guard reports whether f already has a flow running, setting notice to say
// so when it does. Every openSelected* call opens with this, so a control
// already in flight explains itself instead of starting a second one.
func (f *actionFlow) guard(notice *string, label string) bool {
	if f.inFlight {
		*notice = label + " already in flight"
		return true
	}
	return false
}

// open arms f for issueID once the artifact is ready to defer to its process.
func (f *actionFlow) open(issueID string) {
	f.inFlight = true
	f.issueID = issueID
}

// close disarms f, whatever the outcome: a failed artifact open, an empty
// answer, or a finished tracker/engine call all end the flow here.
func (f *actionFlow) close() {
	f.inFlight = false
	f.issueID = ""
}

// applyResult commits a finished tracker/engine call: success or failure both
// land on notice, never silently. label names the control ("approve",
// "answer") for the failure notice; successFmt is a one-verb format string
// taking the Issue ID ("approve requested for %s", "answer posted for %s").
func (f *actionFlow) applyResult(notice *string, issueID string, err error, label, successFmt string) {
	f.close()
	if err == nil {
		*notice = fmt.Sprintf(successFmt, issueID)
		return
	}
	*notice = label + ": " + err.Error()
}

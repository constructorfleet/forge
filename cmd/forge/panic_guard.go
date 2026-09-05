package main

import (
	"fmt"
	"os"
)

// withPanicGuard runs fn and recovers a panic at this command boundary. It
// turns the panic into a printed error and exit code 1, not a bare crash.
// main calls os.Exit on the int a command function returns. A call to
// os.Exit skips every deferred function further up the call stack. So the
// recover belongs here, inside runWatch and runExecute themselves, not in
// main (issue #485).
func withPanicGuard(name string, fn func() int) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "%s: panic: %v\n", name, r)
			code = 1
		}
	}()
	return fn()
}

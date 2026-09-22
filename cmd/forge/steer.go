package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Teagan42/forge/internal/steering"
)

// steerer is the narrow seam doRunSteer enqueues through. steering.Registry
// satisfies it in production; a test double lets doRunSteer's argument
// parsing and output be verified without a real running loop.
type steerer interface {
	Steer(loopID, text string) error
}

type answerer interface {
	Answer(loopID, text string) error
}

// runSteer implements `forge steer <loop-id> <message...>`: it resolves
// loop-id through steering.DefaultRegistry and enqueues a free-form
// steering message or NEEDS_INFO answer onto the Queue registered there.
//
// loop-id is a running execute loop's Execution ID: internal/engine's
// ExecuteInExecution registers that loop's Queue into DefaultRegistry under
// its Execution ID for the loop's duration (constructorfleet/forge#746).
// forge steer only reaches a loop running in the same OS process — a
// forge execute invocation and a separately-invoked forge steer are
// distinct processes with independent DefaultRegistry instances, so a
// loop-id from another process's forge execute still resolves to
// steering.ErrLoopNotFound.
func runSteer(args []string) int {
	return doRunSteer(args, steering.DefaultRegistry, os.Stdout, os.Stderr)
}

// doRunSteer resolves the running loop named by args' first positional
// argument and enqueues the remaining arguments (joined with spaces) as one
// steering Message onto it. The message takes effect only at that loop's
// next step boundary (internal/engine's runRepairLoop drains its Queue
// there, between steps, never mid-step), so doRunSteer always reports the
// message as queued, never applied — even when a step is currently running:
// Steer returns as soon as the message is enqueued, without waiting for that
// step to finish.
func doRunSteer(args []string, s steerer, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("forge steer", flag.ContinueOnError)
	fs.SetOutput(stderr)
	answer := fs.Bool("answer", false, "enqueue a NEEDS_INFO answer instead of a steering message")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 2 {
		fmt.Fprintln(stderr, "forge steer: expected two arguments, <loop-id> <message>")
		return 2
	}
	loopID := fs.Arg(0)
	text := strings.Join(fs.Args()[1:], " ")

	var err error
	if *answer {
		answerer, ok := s.(answerer)
		if !ok {
			fmt.Fprintln(stderr, "forge steer: answer mode is not supported")
			return 1
		}
		err = answerer.Answer(loopID, text)
	} else {
		err = s.Steer(loopID, text)
	}
	if err != nil {
		fmt.Fprintf(stderr, "forge steer: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "message queued for loop %s; it takes effect at that loop's next step boundary\n", loopID)
	return 0
}

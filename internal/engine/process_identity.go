package engine

// Process identity: a pid alone cannot prove that a recorded Worker owner is
// still the same process, because the operating system reuses a pid after its
// process exits. See issue 457.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Teagan42/forge/internal/storage"
)

// ownerToken identifies this Engine's own process for a new claim. It
// returns an empty token when it cannot identify the process, and
// Store.UpdateWorkerOwner then keeps the token already recorded.
func (e *Engine) ownerToken(ctx context.Context) string {
	if e.ProcessStartToken == nil {
		return ""
	}
	pid := e.OwnerPID()
	e.ownerTokenMu.Lock()
	if e.ownerTokenDone && e.ownerTokenPID == pid {
		token := e.ownerTokenValue
		e.ownerTokenMu.Unlock()
		return token
	}
	e.ownerTokenMu.Unlock()

	// The lookup can run a subprocess, so it runs outside the lock. A
	// concurrent duplicate lookup is harmless: both give the same token.
	token := e.ProcessStartToken(ctx, pid)

	e.ownerTokenMu.Lock()
	e.ownerTokenValue = token
	e.ownerTokenPID = pid
	e.ownerTokenDone = true
	e.ownerTokenMu.Unlock()
	return token
}

// ProcessStartToken exposes processStartToken so internal/planengine can
// share this same process-identity lookup for the Feature planning lease
// (issue 557) instead of duplicating the darwin/procfs-specific lookup.
func ProcessStartToken(ctx context.Context, pid int) string {
	return processStartToken(ctx, pid)
}

// claimOwnerIsLive reports whether claim's recorded owner is still the same
// live process. A pid the operating system reused fails the identity test
// and counts as absent, so cancel does not signal an unrelated process and
// recovery does not refuse to resume.
func (e *Engine) claimOwnerIsLive(ctx context.Context, claim storage.WorkerClaim) (bool, error) {
	return OwnerIsLive(ctx, claim.OwnerPID, claim.OwnerToken, e.ProcessRunning, e.ProcessStartToken)
}

// OwnerIsLive reports whether the process at pid is still the same live
// process that recorded ownerToken. A pid the operating system reused fails
// the identity test and counts as absent. internal/planengine shares this
// algorithm for the Feature planning lease (issue 557) instead of
// duplicating it for a second owned resource.
//
// processStartToken may be nil, which falls back to the pid test alone.
func OwnerIsLive(ctx context.Context, pid int, ownerToken string, processRunning func(int) (bool, error), processStartToken func(context.Context, int) string) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	running, err := processRunning(pid)
	if err != nil {
		return false, err
	}
	if !running || ownerToken == "" || processStartToken == nil {
		return running, nil
	}
	token := processStartToken(ctx, pid)
	if token == "" {
		return true, nil
	}
	return token == ownerToken, nil
}

// processStartToken reads pid's start time, which changes when the operating
// system reuses the pid. It tries /proc/<pid>/stat first, which needs no
// external program, then platformStartToken on darwin, which reads
// kinfo_proc.p_starttime through sysctl at microsecond precision, and runs ps
// when both fail. ps runs under LC_ALL=C, because ps formats the start time
// for the locale.
//
// The ps start time has a granularity of one second, so a pid the operating
// system reuses inside the same second can still give the same token. A
// failure returns an empty token: the token only makes the liveness test
// stricter, so its absence must not break cancel or recovery.
func processStartToken(ctx context.Context, pid int) string {
	if pid <= 0 {
		return ""
	}
	if token, err := procStartToken(pid); err == nil {
		return token
	}
	if platformStartToken != nil {
		if token, err := platformStartToken(pid); err == nil {
			return token
		}
	}
	cmd := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.Join(strings.Fields(string(out)), " ")
}

// procStartToken reads field 22 (starttime, in clock ticks since boot) from
// /proc/<pid>/stat. It fails on a system without procfs.
func procStartToken(pid int) (string, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	// The comm field is parenthesized and can hold spaces, so the numbered
	// fields start after the last ')'.
	rest := string(raw)
	end := strings.LastIndex(rest, ")")
	if end < 0 {
		return "", fmt.Errorf("engine: parse /proc/%d/stat", pid)
	}
	fields := strings.Fields(rest[end+1:])
	// starttime is field 22; field 3 (state) is the first one after comm.
	const startTimeIndex = 22 - 3
	if len(fields) <= startTimeIndex {
		return "", fmt.Errorf("engine: parse /proc/%d/stat", pid)
	}
	return fields[startTimeIndex], nil
}

// Package supervise runs a sandbox command so that its timeout is enforced
// inside the sandbox.
//
// Docker has no way to kill an exec'd process: when a run times out, the API
// can only stop waiting for it, and the command kept running in the container.
// The API therefore starts every command through this supervisor (the API
// binary itself, mounted read-only at /.vaultrun/vr-exec): it runs the command
// in a process group of its own and, when the deadline passes, kills that
// whole group, so the command and the children it started stop with it.
//
// A process that deliberately leaves the group (setsid) is not stopped; it
// stays bounded by the sandbox's CPU, memory and process limits and ends with
// the session. Background processes of a command that finishes in time are
// left running, as before: agents start servers for later runs that way.
package supervise

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Name is the file name the supervisor is installed under in sandboxes; the
// API's main dispatches here when it is started under this name.
const Name = "vr-exec"

// Dir is where the supervisor is mounted in sandboxes.
const Dir = "/.vaultrun"

// Path is the supervisor's path inside a sandbox.
const Path = Dir + "/" + Name

// ExitTimeout is the exit status of a command stopped at its deadline (the
// same as coreutils timeout).
const ExitTimeout = 124

// TimeoutMessage starts the line written to stderr when a command is stopped.
const TimeoutMessage = "[vaultrun] command timed out after"

// Args returns the argv that runs command under the supervisor.
func Args(timeout time.Duration, command string, args []string) []string {
	out := []string{Path, "-t", strconv.FormatInt(int64(timeout/time.Millisecond), 10) + "ms", "--", command}
	return append(out, args...)
}

func parse(argv []string) (time.Duration, []string, error) {
	var timeout time.Duration
	for len(argv) > 0 {
		switch argv[0] {
		case "-t":
			if len(argv) < 2 {
				return 0, nil, errors.New("-t needs a duration")
			}
			d, err := time.ParseDuration(argv[1])
			if err != nil {
				return 0, nil, fmt.Errorf("-t: %w", err)
			}
			timeout = d
			argv = argv[2:]
		case "--":
			if len(argv) < 2 {
				return 0, nil, errors.New("no command after --")
			}
			return timeout, argv[1:], nil
		default:
			return 0, nil, fmt.Errorf("usage: %s [-t duration] -- command [args...]", Name)
		}
	}
	return 0, nil, errors.New("no command")
}

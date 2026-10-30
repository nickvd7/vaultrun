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
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
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

// Main runs the supervisor with its arguments (without the program name) and
// returns the exit status.
func Main(argv []string) int {
	timeout, command, err := parse(argv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "vr-exec:", err)
		return 2
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		// Match what Docker reports for an exec that cannot start.
		fmt.Fprintf(os.Stderr, "exec: %q: %v\n", command[0], unwrapExecErr(err))
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return 127
		}
		return 126
	}
	pgid := cmd.Process.Pid

	// Signals to the supervisor (session stop) go to the whole group.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		for s := range sigs {
			_ = syscall.Kill(-pgid, s.(syscall.Signal))
		}
	}()

	var timedOut atomic.Bool
	if timeout > 0 {
		t := time.AfterFunc(timeout, func() {
			timedOut.Store(true)
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		})
		defer t.Stop()
	}

	err = cmd.Wait()
	if timedOut.Load() {
		fmt.Fprintf(os.Stderr, "\n%s %s; it and the processes it started were stopped\n", TimeoutMessage, timeout)
		return ExitTimeout
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return exitErr.ExitCode()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "vr-exec:", err)
		return 126
	}
	return 0
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

func unwrapExecErr(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	var ee *exec.Error
	if errors.As(err, &ee) {
		return ee.Err
	}
	return err
}

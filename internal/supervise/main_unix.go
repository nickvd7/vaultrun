//go:build unix

package supervise

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

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

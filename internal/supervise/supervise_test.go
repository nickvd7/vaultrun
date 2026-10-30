//go:build unix

package supervise

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The tests run the supervisor in a child process (this test binary,
// re-executed), as a sandbox would.
func TestMain(m *testing.M) {
	if args := os.Getenv("VR_SUPERVISE_ARGS"); args != "" {
		os.Exit(Main(strings.Split(args, "\x1f")))
	}
	os.Exit(m.Run())
}

func run(t *testing.T, args ...string) (int, string, time.Duration) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "VR_SUPERVISE_ARGS="+strings.Join(args, "\x1f"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	start := time.Now()
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, stderr.String(), time.Since(start)
}

// alive reports whether pid is running. A killed process stays a zombie until
// its new parent reaps it, which kill(pid, 0) still counts as existing.
func alive(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// The state follows the parenthesised command name.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i >= 0 && i+2 < len(s) && s[i+2] != 'Z'
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	for i := 0; i < 50; i++ {
		if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil {
				t.Fatal(err)
			}
			return pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no pid written")
	return 0
}

func TestExitCodePassesThrough(t *testing.T) {
	if code, _, _ := run(t, "--", "sh", "-c", "exit 3"); code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	if code, _, _ := run(t, "-t", "5s", "--", "true"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestMissingCommandLikeDocker(t *testing.T) {
	code, stderr, _ := run(t, "--", "no-such-command-vr")
	if code != 127 || !strings.Contains(stderr, `exec: "no-such-command-vr": executable file not found in $PATH`) {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	code, stderr, _ = run(t, "--", "/etc/hostname")
	if code != 126 || !strings.Contains(stderr, "permission denied") {
		t.Fatalf("not executable: code=%d stderr=%q", code, stderr)
	}
}

// At the deadline the command and the children it started are killed.
func TestTimeoutKillsTheProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child")
	code, stderr, took := run(t, "-t", "500ms", "--", "sh", "-c", "sleep 60 & echo $! > "+pidFile+"; sleep 60")
	if code != ExitTimeout || !strings.Contains(stderr, TimeoutMessage) {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if took > 10*time.Second {
		t.Fatalf("took %s", took)
	}
	child := readPID(t, pidFile)
	for i := 0; i < 50 && alive(child); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(child) {
		_ = syscall.Kill(child, syscall.SIGKILL)
		t.Fatal("background child survived the timeout")
	}
}

// A command that finishes in time keeps its background processes (agents
// start servers for later runs that way).
func TestBackgroundProcessOutlivesAFinishedCommand(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child")
	code, _, _ := run(t, "-t", "5s", "--", "sh", "-c", "sleep 30 >/dev/null 2>&1 & echo $! > "+pidFile)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	child := readPID(t, pidFile)
	defer syscall.Kill(child, syscall.SIGKILL)
	if !alive(child) {
		t.Fatal("background process of a finished command was killed")
	}
}

func TestArgs(t *testing.T) {
	got := strings.Join(Args(1500*time.Millisecond, "python", []string{"-c", "x"}), " ")
	if got != "/.vaultrun/vr-exec -t 1500ms -- python -c x" {
		t.Fatalf("Args = %q", got)
	}
}

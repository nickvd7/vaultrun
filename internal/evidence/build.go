package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"unicode/utf8"
)

// NewRun fills a Run's output fields: the SHA-256 of the full stored output,
// and the text itself capped at MaxOutputBytes.
func NewRun(r Run, stdout, stderr string) Run {
	r.StdoutSHA256 = SHA256Hex(stdout)
	r.StderrSHA256 = SHA256Hex(stderr)
	r.Stdout, r.StdoutTruncated = capOutput(stdout)
	r.Stderr, r.StderrTruncated = capOutput(stderr)
	if r.Args == nil {
		r.Args = []string{}
	}
	return r
}

// capOutput cuts s to MaxOutputBytes without splitting a UTF-8 sequence.
func capOutput(s string) (string, bool) {
	if len(s) <= MaxOutputBytes {
		return s, false
	}
	cut := MaxOutputBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// HashWorkspace lists the regular files under root with their size and
// SHA-256, sorted by path. Symlinks are listed but never followed, so a
// sandbox cannot point the export at files outside its workspace. At most
// MaxFiles entries are returned; truncated reports whether more existed.
func HashWorkspace(root string) (files []File, truncated bool, err error) {
	files = []File{}
	// Walk and read through an os.Root: the sandbox controls its workspace and
	// could otherwise swap a directory for a symlink mid-walk and have the
	// export describe files outside it.
	wsRoot, err := os.OpenRoot(root)
	if os.IsNotExist(err) {
		// A deleted session has no workspace left; its runs and audit still count.
		return files, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer wsRoot.Close()
	err = fs.WalkDir(wsRoot.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." || d.IsDir() {
			return nil
		}
		if len(files) >= MaxFiles {
			truncated = true
			return filepath.SkipAll
		}
		rel := "/" + path
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			files = append(files, File{Path: rel, Size: info.Size(), Symlink: true})
		case info.Mode().IsRegular():
			sum, size, err := hashFile(wsRoot, path)
			if err != nil {
				return fmt.Errorf("hash %s: %w", rel, err)
			}
			files = append(files, File{Path: rel, Size: size, SHA256: sum})
		}
		// Sockets, devices and pipes are not workspace content.
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, truncated, err
}

func hashFile(root *os.Root, path string) (string, int64, error) {
	// The sandbox can change its workspace while this runs: never follow a
	// symlink swapped in for the file (O_NOFOLLOW), never block on a FIFO
	// (O_NONBLOCK), and only hash what is still a regular file.
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", 0, fmt.Errorf("file changed while it was being exported")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if !st.Mode().IsRegular() {
		return "", 0, fmt.Errorf("file changed while it was being exported")
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Finding is something a verifier should report about a validly signed pack.
type Finding struct {
	Severity string // "warning" or "info"
	Message  string
}

// Check inspects a pack whose signature was verified for content the
// signature cannot vouch for on its own: audit entries the server could not
// verify at export, outputs whose embedded text does not match their hash.
func Check(p *Pack) []Finding {
	var out []Finding
	warn := func(format string, a ...any) {
		out = append(out, Finding{Severity: "warning", Message: fmt.Sprintf(format, a...)})
	}
	info := func(format string, a ...any) {
		out = append(out, Finding{Severity: "info", Message: fmt.Sprintf(format, a...)})
	}

	for _, r := range p.Runs {
		if !r.StdoutTruncated && SHA256Hex(r.Stdout) != r.StdoutSHA256 {
			warn("run %s: stdout does not match its SHA-256", r.ID)
		}
		if !r.StderrTruncated && SHA256Hex(r.Stderr) != r.StderrSHA256 {
			warn("run %s: stderr does not match its SHA-256", r.ID)
		}
	}

	if !p.Audit.Signed {
		warn("audit entries were not signed by the server (AUDIT_HMAC_KEY unset); only this pack's signature protects them")
	} else {
		for _, e := range p.Audit.Entries {
			switch e.SigStatus {
			case "valid":
			case "":
				warn("audit entry %s (%s) has no verification status", e.ID, e.Action)
			default:
				msg := fmt.Sprintf("audit entry %s (%s) was %s at export", e.ID, e.Action, e.SigStatus)
				if e.SigReason != "" {
					msg += ": " + e.SigReason
				}
				warn("%s", msg)
			}
		}
	}
	if p.Audit.Scope == AuditScopeActor {
		info("audit entries are limited to the exporting key's own; export with the master key for all of them")
	}
	if p.Session.Runtime == "" {
		info("sandbox runtime not recorded (daemon default, normally runc)")
	}
	for _, n := range p.Notes {
		info("%s", n)
	}
	return out
}

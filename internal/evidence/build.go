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
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root || d.IsDir() {
			return nil
		}
		if len(files) >= MaxFiles {
			truncated = true
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = "/" + filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			files = append(files, File{Path: rel, Size: info.Size(), Symlink: true})
		case info.Mode().IsRegular():
			sum, err := hashFile(path)
			if err != nil {
				return fmt.Errorf("hash %s: %w", rel, err)
			}
			files = append(files, File{Path: rel, Size: info.Size(), SHA256: sum})
		}
		// Sockets, devices and pipes are not workspace content.
		return nil
	})
	if os.IsNotExist(err) {
		// A deleted session has no workspace left; its runs and audit still count.
		return []File{}, false, nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, truncated, err
}

func hashFile(path string) (string, error) {
	// The sandbox can change its workspace while this runs: re-check the type
	// before opening, and that the opened file is the one that was checked.
	before, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		// Swapped for a symlink or FIFO after the walk; opening a FIFO would block.
		return "", fmt.Errorf("file changed while it was being exported")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(before, after) {
		return "", fmt.Errorf("file changed while it was being exported")
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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

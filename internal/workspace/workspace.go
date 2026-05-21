package workspace

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"
)

type Manager struct {
	baseDir string
}

func New(baseDir string) *Manager {
	return &Manager{baseDir: baseDir}
}

// Create initializes an isolated workspace directory for a session.
// Mode 0o777: the directory is bind-mounted into the sandbox container
// which runs as an unprivileged user (e.g. nobody, UID 65534). That
// user must be able to read uploaded files and create output files inside
// the workspace. The security boundary is the container itself; the base
// workspace directory (one level up) retains tighter permissions so
// other host processes cannot enumerate session UUIDs.
func (m *Manager) Create(sessionID uuid.UUID) (string, error) {
	path := m.sessionPath(sessionID)
	if err := os.MkdirAll(path, 0o777); err != nil {
		return "", fmt.Errorf("create workspace dir: %w", err)
	}
	return path, nil
}

// Delete removes the workspace directory.
func (m *Manager) Delete(sessionID uuid.UUID) error {
	path := m.sessionPath(sessionID)
	return os.RemoveAll(path)
}

// SafePath resolves a user-provided path within the workspace, enforcing:
//  1. No ".." components (after URL-decoding)
//  2. Resolved path stays inside the workspace root
func (m *Manager) SafePath(sessionID uuid.UUID, userPath string) (string, error) {
	// URL-decode first to catch encoded traversals like %2e%2e%2f
	decoded, err := url.PathUnescape(userPath)
	if err != nil {
		decoded = userPath
	}

	// Reject any path containing ".." in raw or decoded form
	if strings.Contains(decoded, "..") || strings.Contains(userPath, "..") {
		return "", fmt.Errorf("path traversal detected")
	}

	root := m.sessionPath(sessionID)
	// Normalize: prepend "/" so Clean can't produce "../" prefixes, then join with root
	cleaned := filepath.Clean("/" + decoded)
	resolved := filepath.Join(root, cleaned)

	// Belt-and-suspenders: verify resolved path is still inside root
	if !strings.HasPrefix(resolved, root+string(os.PathSeparator)) && resolved != root {
		return "", fmt.Errorf("path traversal detected")
	}

	return resolved, nil
}

// safeReadPath extends SafePath with a symlink escape check.
// Used for reads, where the target file already exists.
func (m *Manager) safeReadPath(sessionID uuid.UUID, userPath string) (string, error) {
	resolved, err := m.SafePath(sessionID, userPath)
	if err != nil {
		return "", err
	}

	// Resolve symlinks to their ultimate target and verify it's still inside root.
	// This prevents a container creating /workspace/escape -> /etc/passwd.
	real, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		// File doesn't exist or symlink target is broken — treat as not found
		return "", fmt.Errorf("file not found or inaccessible")
	}

	root := m.sessionPath(sessionID)
	if !strings.HasPrefix(real, root+string(os.PathSeparator)) && real != root {
		return "", fmt.Errorf("symlink escape detected")
	}

	return real, nil
}

// WriteFile writes data to a safe path inside the workspace, creating
// intermediate directories as needed. Mode 0o600: owner-only access.
//
// Security: guards against symlink-escape attacks where a container process
// creates a symlink inside /workspace pointing to a host path outside the
// workspace root. Two layers of protection are applied:
//  1. The entire parent-directory chain is symlink-resolved and verified to
//     stay inside the workspace root before any write takes place.
//  2. The file is opened with O_NOFOLLOW so that if the final path component
//     is itself a symlink (e.g. created in a TOCTOU window) the open fails.
func (m *Manager) WriteFile(sessionID uuid.UUID, userPath string, r io.Reader, maxBytes int64) (int64, error) {
	dest, err := m.SafePath(sessionID, userPath)
	if err != nil {
		return 0, err
	}

	root := m.sessionPath(sessionID)
	parentDir := filepath.Dir(dest)

	// 0o777 so container processes (running as nobody) can traverse and create
	// files in subdirectories they didn't explicitly create.
	if err := os.MkdirAll(parentDir, 0o777); err != nil {
		return 0, fmt.Errorf("create parent dirs: %w", err)
	}

	// Resolve every symlink in the parent directory chain and confirm it stays
	// inside the workspace root. This catches symlinked subdirectories.
	realParent, err := filepath.EvalSymlinks(parentDir)
	if err != nil {
		return 0, fmt.Errorf("resolve parent directory: %w", err)
	}
	if !strings.HasPrefix(realParent, root+string(os.PathSeparator)) && realParent != root {
		return 0, fmt.Errorf("symlink escape detected")
	}

	// Reconstruct dest from the real (non-symlinked) parent path.
	dest = filepath.Join(realParent, filepath.Base(dest))

	// O_NOFOLLOW: if the final path component is a symlink the open returns
	// ELOOP, preventing a race between the parent check above and this open.
	// Mode 0o644: world-readable so container processes (nobody) can read
	// uploaded files; only the API process owner can overwrite them.
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return 0, fmt.Errorf("write target is a symlink — rejected")
		}
		return 0, fmt.Errorf("open file for write: %w", err)
	}
	defer f.Close()

	// Read up to maxBytes exactly. If the reader still has data after that,
	// the file is oversized — delete and return an error.
	lr := io.LimitReader(r, maxBytes)
	n, err := io.Copy(f, lr)
	if err != nil {
		_ = os.Remove(dest)
		return 0, fmt.Errorf("write file: %w", err)
	}
	if n == maxBytes {
		extra := make([]byte, 1)
		if nr, _ := r.Read(extra); nr > 0 {
			_ = os.Remove(dest)
			return 0, fmt.Errorf("file exceeds maximum size of %d bytes", maxBytes)
		}
	}

	return n, nil
}

// OpenFile opens a file inside the workspace for reading.
// Symlink targets are validated to remain inside the workspace.
func (m *Manager) OpenFile(sessionID uuid.UUID, userPath string) (*os.File, error) {
	dest, err := m.safeReadPath(sessionID, userPath)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(dest)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	return f, nil
}

func (m *Manager) sessionPath(sessionID uuid.UUID) string {
	return filepath.Join(m.baseDir, sessionID.String())
}

// SessionPath exposes the workspace root for a session (used by Docker bind mount).
func (m *Manager) SessionPath(sessionID uuid.UUID) string {
	return m.sessionPath(sessionID)
}

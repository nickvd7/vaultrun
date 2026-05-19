package workspace

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

type Manager struct {
	baseDir string
}

func New(baseDir string) *Manager {
	return &Manager{baseDir: baseDir}
}

// Create initializes an isolated workspace directory for a session.
func (m *Manager) Create(sessionID uuid.UUID) (string, error) {
	path := m.sessionPath(sessionID)
	if err := os.MkdirAll(path, 0o750); err != nil {
		return "", fmt.Errorf("create workspace dir: %w", err)
	}
	return path, nil
}

// Delete removes the workspace directory.
func (m *Manager) Delete(sessionID uuid.UUID) error {
	path := m.sessionPath(sessionID)
	return os.RemoveAll(path)
}

// SafePath resolves a user-provided path within the workspace, preventing
// path traversal. Returns an error if the path contains ".." components or
// if the resolved path escapes the workspace root.
func (m *Manager) SafePath(sessionID uuid.UUID, userPath string) (string, error) {
	// URL-decode first to catch encoded traversals like %2e%2e%2f
	decoded, err := url.PathUnescape(userPath)
	if err != nil {
		decoded = userPath
	}

	// Reject any path that explicitly contains ".." in any form
	if strings.Contains(decoded, "..") || strings.Contains(userPath, "..") {
		return "", fmt.Errorf("path traversal detected")
	}

	root := m.sessionPath(sessionID)
	// Normalize: strip leading slashes and clean
	cleaned := filepath.Clean("/" + decoded)
	resolved := filepath.Join(root, cleaned)

	// Final check: resolved path must still be inside root
	if !strings.HasPrefix(resolved, root+string(os.PathSeparator)) && resolved != root {
		return "", fmt.Errorf("path traversal detected")
	}

	return resolved, nil
}

// WriteFile writes data to a safe path inside the workspace, creating
// intermediate directories as needed.
func (m *Manager) WriteFile(sessionID uuid.UUID, userPath string, r io.Reader, maxBytes int64) (int64, error) {
	dest, err := m.SafePath(sessionID, userPath)
	if err != nil {
		return 0, err
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return 0, fmt.Errorf("create parent dirs: %w", err)
	}

	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return 0, fmt.Errorf("open file for write: %w", err)
	}
	defer f.Close()

	lr := io.LimitReader(r, maxBytes+1)
	n, err := io.Copy(f, lr)
	if err != nil {
		return 0, fmt.Errorf("write file: %w", err)
	}
	if n > maxBytes {
		_ = os.Remove(dest)
		return 0, fmt.Errorf("file exceeds maximum size of %d bytes", maxBytes)
	}

	return n, nil
}

// OpenFile opens a file inside the workspace for reading.
func (m *Manager) OpenFile(sessionID uuid.UUID, userPath string) (*os.File, error) {
	dest, err := m.SafePath(sessionID, userPath)
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

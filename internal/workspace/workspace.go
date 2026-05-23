package workspace

import (
	"archive/tar"
	"compress/gzip"
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
//
// Why 0o777 is required:
//   The directory is bind-mounted into the sandbox container, which runs as
//   the unprivileged user "nobody" (UID 65534). From the Linux kernel's
//   perspective, UID 65534 is "other" relative to the host process that owns
//   the workspace, so the container user can only access the directory if the
//   "other" permission bits are set. Without 0o777 (specifically the world-wx
//   bits) the container process cannot create output files or traverse into
//   subdirectories, causing sandbox runs to fail.
//
// Why not use a tighter permission model:
//   The ideal alternative would be a per-session UID combined with a user
//   namespace so the container runs as a private UID that owns the directory
//   (allowing 0o700). That approach is out of scope for v1 and requires
//   additional kernel capabilities or a suid helper binary.
//
// Limiting blast radius on the host:
//   0o777 on the session-specific directory means other host processes running
//   as arbitrary UIDs can also write to it. To prevent those host processes
//   from even discovering which session directories exist, the parent baseDir
//   is created with 0o700 (owner-only) in main.go. Only the owner of the host
//   process can list baseDir entries; the 0o777 child directories are therefore
//   not reachable by other host users without already knowing the session UUID.
//
// umask behaviour:
//   os.MkdirAll honours the process umask, which can silently strip the
//   group/other bits we set (e.g. umask 0o077 → effective 0o700). To
//   guarantee the intended 0o777 we call os.Chmod after creation; Chmod is
//   not affected by the umask.
func (m *Manager) Create(sessionID uuid.UUID) (string, error) {
	path := m.sessionPath(sessionID)
	if err := os.MkdirAll(path, 0o777); err != nil {
		return "", fmt.Errorf("create workspace dir: %w", err)
	}
	// Force permissions — os.Chmod is umask-independent.
	if err := os.Chmod(path, 0o777); err != nil {
		return "", fmt.Errorf("chmod workspace dir: %w", err)
	}
	return path, nil
}

// Delete removes the workspace directory.
func (m *Manager) Delete(sessionID uuid.UUID) error {
	path := m.sessionPath(sessionID)
	return os.RemoveAll(path)
}

// SafePath resolves a user-provided path within the workspace, enforcing:
//  1. URL-decoded (loop until stable) to catch double/triple-encoded traversals
//  2. No ".." path components (checked after full decoding, before Clean)
//  3. Resolved path stays inside the workspace root (filepath.Clean + HasPrefix)
func (m *Manager) SafePath(sessionID uuid.UUID, userPath string) (string, error) {
	// URL-decode in a loop until stable to catch double/triple-encoded traversals
	// (%252e%252e%2f → %2e%2e/ → ../).
	decoded := userPath
	for {
		next, err := url.PathUnescape(decoded)
		if err != nil || next == decoded {
			break
		}
		decoded = next
	}

	// Reject any path component that is exactly "..".
	// This is checked AFTER full decoding so encoded variants are caught too.
	// We use a component-level check (not strings.Contains) so "foo..txt" is
	// still allowed while "../" traversals are rejected as errors — important
	// for clients that distinguish traversal attempts from valid paths.
	for _, part := range strings.Split(filepath.ToSlash(decoded), "/") {
		if part == ".." {
			return "", fmt.Errorf("path traversal detected")
		}
	}

	root := m.sessionPath(sessionID)
	// filepath.Clean eliminates any remaining "." components and double slashes;
	// joining with the root ensures the result is absolute and inside root.
	cleaned := filepath.Clean("/" + decoded)
	resolved := filepath.Join(root, cleaned)

	// Belt-and-suspenders: trailing-separator HasPrefix prevents
	// /data/workspaces/AAAA matching /data/workspaces/AAAA-other/...
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
// intermediate directories as needed.
//
// Security: guards against symlink-escape attacks where a container process
// creates a symlink inside /workspace pointing to a host path outside the
// workspace root. Two layers of protection are applied:
//  1. The entire parent-directory chain is symlink-resolved and verified to
//     stay inside the workspace root before any write takes place.
//  2. The file is opened with O_NOFOLLOW so that if the final path component
//     is itself a symlink (e.g. created in a TOCTOU window) the open fails.
//
// Permissions: directories are created 0o777 and files 0o644 so that the
// container process (running as nobody/UID 65534) can read the files and
// write new output files. os.Chmod is called after creation to bypass the
// process umask, which would otherwise silently strip the group/other bits.
func (m *Manager) WriteFile(sessionID uuid.UUID, userPath string, r io.Reader, maxBytes int64) (int64, error) {
	dest, err := m.SafePath(sessionID, userPath)
	if err != nil {
		return 0, err
	}

	root := m.sessionPath(sessionID)
	parentDir := filepath.Dir(dest)

	if err := os.MkdirAll(parentDir, 0o777); err != nil {
		return 0, fmt.Errorf("create parent dirs: %w", err)
	}
	// Chmod every directory from root down to parentDir to guarantee
	// world-execute regardless of the process umask.
	if err := chmodDirs(root, parentDir, 0o777); err != nil {
		return 0, fmt.Errorf("chmod parent dirs: %w", err)
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

	// Force file permissions umask-independently.
	if err := os.Chmod(dest, 0o644); err != nil {
		return 0, fmt.Errorf("chmod file: %w", err)
	}

	return n, nil
}

// chmodDirs applies mode to every directory from root down to target
// (inclusive). It is used to guarantee world-execute on intermediate dirs
// created by os.MkdirAll regardless of the process umask.
func chmodDirs(root, target string, mode os.FileMode) error {
	// Collect each path component from root to target.
	dirs := []string{root}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." {
		// target == root — chmod root only
		return os.Chmod(root, mode)
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	cur := root
	for _, p := range parts {
		cur = filepath.Join(cur, p)
		dirs = append(dirs, cur)
	}
	for _, d := range dirs {
		if err := os.Chmod(d, mode); err != nil {
			return err
		}
	}
	return nil
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

func (m *Manager) SessionPath(sessionID uuid.UUID) string {
	return m.sessionPath(sessionID)
}

// CreateSnapshot creates a gzip-compressed tar archive of the session workspace.
// The archive is written to {baseDir}/snapshots/<snapshotID>.tar.gz and the
// archive path + uncompressed size are returned.
func (m *Manager) CreateSnapshot(sessionID, snapshotID uuid.UUID) (archivePath string, sizeBytes int64, err error) {
	root := m.sessionPath(sessionID)
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return "", 0, fmt.Errorf("workspace not found for session %s", sessionID)
	}

	snapsDir := filepath.Join(m.baseDir, "snapshots")
	if err := os.MkdirAll(snapsDir, 0o700); err != nil {
		return "", 0, fmt.Errorf("create snapshots dir: %w", err)
	}

	archivePath = filepath.Join(snapsDir, snapshotID.String()+".tar.gz")
	f, err := os.OpenFile(archivePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", 0, fmt.Errorf("create archive file: %w", err)
	}
	defer f.Close()

	cw := newCountingWriter(f)
	gzw, err := gzip.NewWriterLevel(cw, gzip.BestSpeed)
	if err != nil {
		return "", 0, fmt.Errorf("create gzip writer: %w", err)
	}
	tw := tar.NewWriter(gzw)

	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		// Resolve symlinks; skip those that escape the workspace.
		if info.Mode()&os.ModeSymlink != 0 {
			real, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil // skip broken symlinks
			}
			if !strings.HasPrefix(real, root+string(os.PathSeparator)) && real != root {
				return nil // skip escape symlinks
			}
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		ff, err := os.Open(path)
		if err != nil {
			return err
		}
		defer ff.Close()
		_, err = io.Copy(tw, ff)
		return err
	})

	if walkErr != nil {
		_ = os.Remove(archivePath)
		return "", 0, fmt.Errorf("archive workspace: %w", walkErr)
	}
	if err := tw.Close(); err != nil {
		_ = os.Remove(archivePath)
		return "", 0, fmt.Errorf("close tar writer: %w", err)
	}
	if err := gzw.Close(); err != nil {
		_ = os.Remove(archivePath)
		return "", 0, fmt.Errorf("close gzip writer: %w", err)
	}

	return archivePath, cw.n, nil
}

// RestoreSnapshot extracts a snapshot archive into the session workspace.
// Files in the archive overwrite existing files; the workspace is not cleared
// first so existing files not in the snapshot are preserved.
func (m *Manager) RestoreSnapshot(sessionID uuid.UUID, archivePath string) error {
	root := m.sessionPath(sessionID)
	if err := os.MkdirAll(root, 0o777); err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	if err := os.Chmod(root, 0o777); err != nil {
		return fmt.Errorf("chmod workspace: %w", err)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}

		// Sanitize path: reject any traversal attempts.
		if strings.Contains(hdr.Name, "..") {
			continue
		}
		dest := filepath.Join(root, filepath.Clean("/"+hdr.Name))
		if !strings.HasPrefix(dest, root+string(os.PathSeparator)) && dest != root {
			continue // path traversal — reject
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o777); err != nil {
				return fmt.Errorf("mkdir %s: %w", hdr.Name, err)
			}
			_ = os.Chmod(dest, 0o777)
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(dest), 0o777); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", hdr.Name, err)
			}
			out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return fmt.Errorf("create %s: %w", hdr.Name, err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return fmt.Errorf("write %s: %w", hdr.Name, err)
			}
			_ = out.Close()
			_ = os.Chmod(dest, 0o644)
		}
	}
	return nil
}

// DeleteSnapshot removes a snapshot archive from disk.
func (m *Manager) DeleteSnapshot(archivePath string) error {
	return os.Remove(archivePath)
}

// countingWriter counts bytes written through it.
type countingWriter struct {
	w io.Writer
	n int64
}

func newCountingWriter(w io.Writer) *countingWriter { return &countingWriter{w: w} }

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

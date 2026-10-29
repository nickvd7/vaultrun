package workspace

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/internal/sandboxuser"
)

type Manager struct {
	baseDir string
}

func New(baseDir string) *Manager {
	return &Manager{baseDir: baseDir}
}

// Create initializes an isolated workspace directory for a session.
//
// os.MkdirAll honours the process umask, which can strip permission bits; the
// explicit os.Chmod afterwards is umask-independent.
func (m *Manager) Create(sessionID uuid.UUID) (string, error) {
	path := m.sessionPath(sessionID)
	if err := os.MkdirAll(path, sandboxuser.DirMode()); err != nil {
		return "", fmt.Errorf("create workspace dir: %w", err)
	}
	// Force permissions — os.Chmod is umask-independent.
	if err := os.Chmod(path, sandboxuser.DirMode()); err != nil {
		return "", fmt.Errorf("chmod workspace dir: %w", err)
	}
	return path, nil
}

// Delete removes the workspace directory.
func (m *Manager) Delete(sessionID uuid.UUID) error {
	path := m.sessionPath(sessionID)
	return os.RemoveAll(path)
}

// ErrPathTraversal is returned for a path with a ".." component.
var ErrPathTraversal = errors.New("path traversal detected")

// CheckPath rejects a user-provided path that contains a ".." component and
// returns the path with percent-encoding fully decoded. Callers use it to
// answer with an error instead of quietly rewriting "../x" to "/x".
//
// The path is URL-decoded in a loop until stable to catch double/triple-encoded
// traversals (%252e%252e%2f), and the check is per component, not
// strings.Contains, so "foo..txt" stays valid.
func CheckPath(userPath string) (string, error) {
	decoded := userPath
	for {
		next, err := url.PathUnescape(decoded)
		if err != nil || next == decoded {
			break
		}
		decoded = next
	}
	for _, part := range strings.Split(filepath.ToSlash(decoded), "/") {
		if part == ".." {
			return "", ErrPathTraversal
		}
	}
	return decoded, nil
}

// SafePath resolves a user-provided path within the workspace, enforcing:
//  1. URL-decoded (loop until stable) to catch double/triple-encoded traversals
//  2. No ".." path components (checked after full decoding, before Clean)
//  3. Resolved path stays inside the workspace root (filepath.Clean + HasPrefix)
func (m *Manager) SafePath(sessionID uuid.UUID, userPath string) (string, error) {
	decoded, err := CheckPath(userPath)
	if err != nil {
		return "", err
	}

	root := m.sessionPath(sessionID)
	// filepath.Clean eliminates any remaining "." components and double slashes;
	// joining with the root ensures the result is absolute and inside root.
	cleaned := filepath.Clean("/" + decoded)
	resolved := filepath.Join(root, cleaned)

	// Belt-and-suspenders: trailing-separator HasPrefix prevents
	// /data/workspaces/AAAA matching /data/workspaces/AAAA-other/...
	if !strings.HasPrefix(resolved, root+string(os.PathSeparator)) && resolved != root {
		return "", ErrPathTraversal
	}

	return resolved, nil
}

// openRoot opens the session's workspace as an os.Root. Every file access
// below goes through it: the kernel resolves each path component relative to
// the workspace and refuses to leave it, through "..", an absolute symlink or
// a symlink to a directory outside it. Checking a path first (EvalSymlinks)
// and opening it afterwards is not enough: the sandbox controls its workspace
// and can swap a directory for a symlink in between, making the API open its
// own /proc/self/environ (with every secret it holds) or another session's
// files.
func (m *Manager) openRoot(sessionID uuid.UUID) (*os.Root, error) {
	return os.OpenRoot(m.sessionPath(sessionID))
}

// Root opens the session's workspace as an os.Root, for callers that walk or
// read it themselves. The caller closes it.
func (m *Manager) Root(sessionID uuid.UUID) (*os.Root, error) { return m.openRoot(sessionID) }

// relPath turns a user path into a path relative to the workspace root, or
// "." for the root itself, after the same checks as SafePath.
func relPath(userPath string) (string, error) {
	decoded, err := CheckPath(userPath)
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean("/" + decoded)
	if cleaned == "/" {
		return ".", nil
	}
	return strings.TrimPrefix(cleaned, "/"), nil
}

// ErrNotFound means a workspace path does not exist, or leads out of the
// workspace (which is reported the same way, so it is no oracle for host
// paths).
var ErrNotFound = errors.New("file not found or inaccessible")

// IsSymlink reports whether userPath is a symlink inside the workspace.
// It uses Lstat, so the link itself is what is checked, not its target.
// A path that escapes the workspace, or does not exist, is not a symlink.
func (m *Manager) IsSymlink(sessionID uuid.UUID, userPath string) bool {
	rel, err := relPath(userPath)
	if err != nil || rel == "." {
		return false
	}
	root, err := m.openRoot(sessionID)
	if err != nil {
		return false
	}
	defer root.Close()
	info, err := root.Lstat(rel)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSymlink != 0
}

// Exists reports whether userPath refers to a real file/dir inside the
// session workspace. Symlinks that escape the workspace root are treated as
// absent (not an oracle for host paths).
func (m *Manager) Exists(sessionID uuid.UUID, userPath string) (bool, error) {
	rel, err := relPath(userPath)
	if err != nil {
		return false, err
	}
	root, err := m.openRoot(sessionID)
	if err != nil {
		return false, nil
	}
	defer root.Close()
	if _, err := root.Stat(rel); err != nil {
		return false, nil
	}
	return true, nil
}

// WriteFile writes data to a safe path inside the workspace, creating
// intermediate directories as needed.
//
// Security: every step goes through the workspace's os.Root, so a symlink the
// sandbox planted (or swaps in while this runs) cannot redirect the write
// outside the workspace; a final component that is a symlink is refused
// outright (O_NOFOLLOW).
//
// Permissions: directories use DirMode and files 0o644, set with Chmod so the
// result does not depend on the process umask.
func (m *Manager) WriteFile(sessionID uuid.UUID, userPath string, r io.Reader, maxBytes int64) (int64, error) {
	rel, err := relPath(userPath)
	if err != nil {
		return 0, err
	}
	if rel == "." {
		return 0, ErrIsDirectory
	}
	root, err := m.openRoot(sessionID)
	if err != nil {
		return 0, fmt.Errorf("open workspace: %w", err)
	}
	defer root.Close()

	// Follow the mode of the session directory: sessions created before v0.4.7
	// are 0777 because their sandbox runs as "nobody", and tightening them to
	// 0700 here would lock that sandbox out of its own workspace.
	dirMode := sandboxuser.DirMode()
	if info, err := root.Stat("."); err == nil {
		dirMode = info.Mode().Perm()
	}
	parent := filepath.Dir(rel)
	if parent != "." {
		if err := root.MkdirAll(parent, dirMode); err != nil {
			if isEscape(err) {
				return 0, fmt.Errorf("symlink escape detected")
			}
			return 0, fmt.Errorf("create parent dirs: %w", err)
		}
	}
	if err := chmodDirs(root, parent, dirMode); err != nil {
		return 0, fmt.Errorf("chmod parent dirs: %w", err)
	}

	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return 0, fmt.Errorf("write target is a symlink — rejected")
		}
		if isEscape(err) {
			return 0, fmt.Errorf("symlink escape detected")
		}
		return 0, fmt.Errorf("open file for write: %w", err)
	}
	defer f.Close()

	// Read up to maxBytes exactly. If the reader still has data after that,
	// the file is oversized — delete and return an error.
	lr := io.LimitReader(r, maxBytes)
	n, err := io.Copy(f, lr)
	if err != nil {
		_ = root.Remove(rel)
		return 0, fmt.Errorf("write file: %w", err)
	}
	if n == maxBytes {
		extra := make([]byte, 1)
		if nr, _ := r.Read(extra); nr > 0 {
			_ = root.Remove(rel)
			return 0, fmt.Errorf("file exceeds maximum size of %d bytes", maxBytes)
		}
	}

	// Force file permissions umask-independently (on the open file, not by
	// path, so a swapped-in symlink cannot redirect the chmod).
	if err := f.Chmod(0o644); err != nil {
		return 0, fmt.Errorf("chmod file: %w", err)
	}

	return n, nil
}

// blocked reports whether err means something the sandbox put in the
// workspace (a symlink, or a file where a directory belongs) is in the way of
// a restore; that entry is skipped instead of written through.
func blocked(err error) bool {
	return isEscape(err) || errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EEXIST) || errors.Is(err, syscall.ENOTDIR)
}

// isEscape reports whether err is os.Root refusing a path that leads out of
// the root.
func isEscape(err error) bool {
	return err != nil && strings.Contains(err.Error(), "path escapes from parent")
}

// chmodDirs applies mode to the workspace root and every directory down to
// rel (inclusive), through the workspace's os.Root. It guarantees
// world-execute on intermediate dirs created by MkdirAll regardless of the
// process umask.
func chmodDirs(root *os.Root, rel string, mode os.FileMode) error {
	dirs := []string{"."}
	if rel != "." {
		cur := ""
		for _, p := range strings.Split(rel, string(os.PathSeparator)) {
			cur = filepath.Join(cur, p)
			dirs = append(dirs, cur)
		}
	}
	for _, d := range dirs {
		// A directory created by a sandbox running as another user (older
		// sessions) cannot be chmodded by the API; it is already usable by it.
		if err := root.Chmod(d, mode); err != nil && !errors.Is(err, os.ErrPermission) {
			return err
		}
	}
	return nil
}

// ErrIsDirectory is returned by OpenFile when the path is a directory.
var ErrIsDirectory = errors.New("path is a directory")

// OpenFile opens a regular file inside the workspace for reading.
// Symlink targets are validated to remain inside the workspace and
// directories are rejected with ErrIsDirectory.
func (m *Manager) OpenFile(sessionID uuid.UUID, userPath string) (*os.File, error) {
	rel, err := relPath(userPath)
	if err != nil {
		return nil, err
	}
	root, err := m.openRoot(sessionID)
	if err != nil {
		return nil, ErrNotFound
	}
	defer root.Close()

	// The file stays open after the root is closed. O_NONBLOCK keeps a FIFO
	// the sandbox planted from blocking the request; it is rejected below.
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrNotFound
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		f.Close()
		return nil, ErrIsDirectory
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, ErrNotFound
	}
	return f, nil
}

// Remove deletes a file (or empty directory) inside the workspace. The parent
// directory chain is symlink-resolved and must stay inside the workspace root,
// so a symlinked directory cannot be used to delete files elsewhere. A symlink
// in the final component is removed itself, never followed.
func (m *Manager) Remove(sessionID uuid.UUID, userPath string) error {
	rel, err := relPath(userPath)
	if err != nil {
		return err
	}
	if rel == "." {
		return fmt.Errorf("cannot remove workspace root")
	}
	root, err := m.openRoot(sessionID)
	if err != nil {
		return ErrNotFound
	}
	defer root.Close()
	if err := root.Remove(rel); err != nil {
		if isEscape(err) {
			return fmt.Errorf("symlink escape detected")
		}
		return err
	}
	return nil
}

func (m *Manager) sessionPath(sessionID uuid.UUID) string {
	return filepath.Join(m.baseDir, sessionID.String())
}

// SessionDirs returns the session IDs that have a directory under the base
// directory. Entries whose name is not a UUID (snapshots/, warm pool) are skipped.
func (m *Manager) SessionDirs() ([]uuid.UUID, error) {
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if id, err := uuid.Parse(e.Name()); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
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

	// Walk and read through the workspace's os.Root so the sandbox cannot
	// swap a directory for a symlink mid-walk and have host files archived.
	// Symlinks are not archived (restore never recreated them).
	wsRoot, err := os.OpenRoot(root)
	if err != nil {
		_ = os.Remove(archivePath)
		return "", 0, fmt.Errorf("open workspace: %w", err)
	}
	defer wsRoot.Close()
	walkErr := fs.WalkDir(wsRoot.FS(), ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if rel == "." {
			return nil
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil // symlinks, sockets, devices, pipes
		}
		info, err := d.Info()
		if err != nil {
			return nil // removed meanwhile
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if !info.Mode().IsRegular() {
			return tw.WriteHeader(hdr)
		}

		ff, err := wsRoot.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil // replaced by a symlink or removed meanwhile
		}
		defer ff.Close()
		st, err := ff.Stat()
		if err != nil || !st.Mode().IsRegular() {
			return nil
		}
		// Archive what is actually there now, which may differ in size.
		hdr.Size = st.Size()
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = io.Copy(tw, io.LimitReader(ff, st.Size()))
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
	rootPath := m.sessionPath(sessionID)
	if err := os.MkdirAll(rootPath, sandboxuser.DirMode()); err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	if err := os.Chmod(rootPath, sandboxuser.DirMode()); err != nil {
		return fmt.Errorf("chmod workspace: %w", err)
	}
	// Everything below writes through the workspace's os.Root: the sandbox may
	// have replaced a directory named in the archive with a symlink to another
	// session's workspace or to an API directory.
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open workspace: %w", err)
	}
	defer root.Close()

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

	const (
		maxExtractFileBytes  int64 = 2 * 1024 * 1024 * 1024  // 2 GB per file
		maxExtractTotalBytes int64 = 10 * 1024 * 1024 * 1024 // 10 GB total
	)
	var totalExtracted int64

	const maxExtractEntries = 200_000 // guard against inode-exhaustion via many tiny entries
	var entryCount int

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}

		entryCount++
		if entryCount > maxExtractEntries {
			return fmt.Errorf("archive exceeds maximum entry count (%d)", maxExtractEntries)
		}

		// Sanitize path: reject any traversal attempts.
		dest, err := relPath(hdr.Name)
		if err != nil || dest == "." {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(dest, sandboxuser.DirMode()); err != nil {
				if blocked(err) {
					continue // a symlink or file is in the way
				}
				return fmt.Errorf("mkdir %s: %w", hdr.Name, err)
			}
			_ = root.Chmod(dest, sandboxuser.DirMode())
		case tar.TypeReg, tar.TypeRegA:
			if parent := filepath.Dir(dest); parent != "." {
				if err := root.MkdirAll(parent, sandboxuser.DirMode()); err != nil {
					if blocked(err) {
						continue
					}
					return fmt.Errorf("mkdir parent of %s: %w", hdr.Name, err)
				}
			}
			out, err := root.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0o644)
			if err != nil {
				if blocked(err) {
					continue // a symlink is in the way; never write through it
				}
				return fmt.Errorf("create %s: %w", hdr.Name, err)
			}
			remaining := maxExtractTotalBytes - totalExtracted
			if remaining <= 0 {
				_ = out.Close()
				return fmt.Errorf("archive extraction limit exceeded (%d GB total)", maxExtractTotalBytes/1024/1024/1024)
			}
			perFileCap := maxExtractFileBytes
			if remaining < perFileCap {
				perFileCap = remaining
			}
			n, copyErr := io.Copy(out, io.LimitReader(tr, perFileCap+1))
			_ = out.Chmod(0o644)
			_ = out.Close()
			if copyErr != nil {
				return fmt.Errorf("write %s: %w", hdr.Name, copyErr)
			}
			if n > perFileCap {
				return fmt.Errorf("file %s exceeds extraction size limit", hdr.Name)
			}
			totalExtracted += n
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

// Local filesystem tools for the MCP server.
// Access is gated by MCP_FS_ALLOWED_PATHS (comma-separated absolute paths).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const fsMaxReadBytes = 10 * 1024 * 1024 // 10 MB

// fsConfig holds the set of allowed root paths for local filesystem access.
type fsConfig struct {
	allowedPaths []string
}

// loadFSConfig parses MCP_FS_ALLOWED_PATHS from the environment.
// Returns an empty fsConfig (filesystem access disabled) when the variable is unset or empty.
func loadFSConfig() fsConfig {
	raw := os.Getenv("MCP_FS_ALLOWED_PATHS")
	if raw == "" {
		return fsConfig{}
	}
	var paths []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Resolve symlinks on the allowed root so that both sides of the
		// prefix comparison in checkFSAccess use canonical paths. Without
		// this, a symlinked allowed path (e.g. /data/safe → /) would grant
		// access to any path the symlink target contains.
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		paths = append(paths, p)
	}
	return fsConfig{allowedPaths: paths}
}

// checkFSAccess validates that path is absolute and falls within one of the
// allowed roots. It resolves symlinks on the parent directory (not the file
// itself, which may not exist yet for writes) before comparing against roots.
// Returns the cleaned path or an error.
func (cfg fsConfig) checkFSAccess(path string) (string, error) {
	if len(cfg.allowedPaths) == 0 {
		return "", fmt.Errorf("local filesystem access is disabled (MCP_FS_ALLOWED_PATHS not set)")
	}
	if path == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path %q must be absolute", path)
	}

	// Reject ".." components defensively before we do anything with the path.
	clean := filepath.Clean(path)
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == ".." {
			return "", fmt.Errorf("path %q: directory traversal not allowed", path)
		}
	}

	// Resolve symlinks on the parent directory. The file itself may not exist
	// yet (e.g. for fs_write_file), so we work one level up.
	parent := filepath.Dir(clean)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		// Parent does not exist — use the cleaned path directly; access check
		// below will still catch escapes.
		resolvedParent = parent
	}
	resolved := filepath.Join(resolvedParent, filepath.Base(clean))

	for _, allowed := range cfg.allowedPaths {
		allowedClean := filepath.Clean(allowed)
		if resolved == allowedClean || strings.HasPrefix(resolved, allowedClean+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("path %q is outside the allowed filesystem roots", path)
}

// ── Tool handlers ─────────────────────────────────────────────────────────────

func (s *server) toolFsReadFile(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	path := args["path"]
	resolved, err := s.fsConfig.checkFSAccess(path)
	if err != nil {
		return mcpToolResult{}, err
	}

	f, err := os.Open(resolved)
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("open %q: %w", resolved, err)
	}
	defer f.Close()

	// Enforce the 10 MB read limit.
	limited := io.LimitReader(f, fsMaxReadBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("read %q: %w", resolved, err)
	}
	if int64(len(data)) > fsMaxReadBytes {
		return mcpToolResult{}, fmt.Errorf("file %q exceeds maximum read size of 10 MB", resolved)
	}

	return textResult(string(data)), nil
}

func (s *server) toolFsWriteFile(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	path := args["path"]
	content := args["content"]

	resolved, err := s.fsConfig.checkFSAccess(path)
	if err != nil {
		return mcpToolResult{}, err
	}

	// Create parent directories as needed.
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return mcpToolResult{}, fmt.Errorf("create parent directories for %q: %w", resolved, err)
	}

	if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
		return mcpToolResult{}, fmt.Errorf("write %q: %w", resolved, err)
	}

	return textResult(fmt.Sprintf("Written %d bytes to %s", len(content), resolved)), nil
}

func (s *server) toolFsListDir(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	path := args["path"]
	resolved, err := s.fsConfig.checkFSAccess(path)
	if err != nil {
		return mcpToolResult{}, err
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("list directory %q: %w", resolved, err)
	}
	if len(entries) == 0 {
		return textResult("Directory is empty."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d entries in %s:\n", len(entries), resolved)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			fmt.Fprintf(&sb, "  %s  [error reading info]\n", entry.Name())
			continue
		}
		entryType := "file"
		size := info.Size()
		if entry.IsDir() {
			entryType = "dir"
			size = 0
		}
		fmt.Fprintf(&sb, "  %s  type=%s  size=%d  modified=%s\n",
			entry.Name(), entryType, size, info.ModTime().Format("2006-01-02 15:04:05"))
	}
	return textResult(sb.String()), nil
}

func (s *server) toolFsDeleteFile(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	path := args["path"]
	resolved, err := s.fsConfig.checkFSAccess(path)
	if err != nil {
		return mcpToolResult{}, err
	}

	if err := os.Remove(resolved); err != nil {
		return mcpToolResult{}, fmt.Errorf("delete %q: %w", resolved, err)
	}
	return textResult(fmt.Sprintf("Deleted %s", resolved)), nil
}

package localgateway

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

// sanitizeWorkspacePath rejects path traversal and absolute paths. VaultRun
// also enforces this server-side; we fail closed here so bad tool args never
// leave the gateway.
func sanitizeWorkspacePath(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("path is required")
	}
	if !utf8.ValidString(raw) {
		return "", fmt.Errorf("path is not valid UTF-8")
	}
	if strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("path contains null byte")
	}
	if strings.Contains(raw, `\`) {
		return "", fmt.Errorf("backslashes are not allowed in paths")
	}
	// Reject ".." in the raw input before Clean rewrites it (e.g. "../etc/passwd"
	// becomes "etc/passwd" after Clean — still reject as traversal intent).
	for _, part := range strings.Split(strings.ReplaceAll(raw, `\`, "/"), "/") {
		if part == ".." {
			return "", fmt.Errorf("path traversal is not allowed")
		}
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(raw, "/"))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" || cleaned == "." {
		return "", fmt.Errorf("path resolves to empty")
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", fmt.Errorf("path traversal is not allowed")
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == ".." || part == "." {
			return "", fmt.Errorf("path traversal is not allowed")
		}
		if part == "" {
			return "", fmt.Errorf("empty path segment")
		}
	}
	return cleaned, nil
}

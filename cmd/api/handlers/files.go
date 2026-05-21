package handlers

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/metrics"
	"github.com/nickvd7/vaultrun/internal/models"
)

type FileHandler struct {
	h *Hub
}

func NewFileHandler(h *Hub) *FileHandler { return &FileHandler{h: h} }

// POST /api/v1/sessions/:id/files
func (fh *FileHandler) Upload(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	// C-2: verify caller owns the session (returns 404 for cross-tenant access).
	session, ok := fh.h.checkSessionAccess(c, sessionID)
	if !ok {
		return
	}
	if session.StoppedAt != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "session is stopped"})
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file field required"})
		return
	}

	// Respect client-specified path or fall back to filename
	userPath := c.PostForm("path")
	if userPath == "" {
		userPath = fileHeader.Filename
	}
	// Normalize before policy eval so rules see the same canonical form that
	// the filesystem will actually use (prevents ./foo/../secret bypass).
	userPath = filepath.Clean("/" + userPath)

	maxBytes := fh.h.cfg.Workspace.MaxFileMB * 1024 * 1024

	// Enforce total workspace size cap when MAX_WORKSPACE_MB > 0.
	// We sum sizes of all existing files for the session (from the DB) and
	// reject the upload if adding the new file would exceed the cap.
	if cap := fh.h.cfg.Workspace.MaxWorkspaceMB; cap > 0 {
		total, err := dbpkg.SumWorkspaceBytes(c.Request.Context(), fh.h.db, sessionID)
		if err != nil {
			slog.Warn("upload: workspace size check failed", "session_id", sessionID, "err", err)
		} else if total+fileHeader.Size > cap*1024*1024 {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": fmt.Sprintf("workspace size cap of %d MB exceeded", cap),
			})
			return
		}
	}

	if d := fh.h.policy.EvalFileAccess(c.Request.Context(), sessionID, userPath, true); !d.Allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "file access denied by policy"})
		return
	}

	f, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open upload"})
		return
	}
	defer f.Close()

	// M-2: Probe actual content type from the first 512 bytes of the upload
	// instead of trusting the client-supplied Content-Type header, which can
	// be spoofed to bypass downstream content-sniffing defences.
	sniff := make([]byte, 512)
	n, _ := f.Read(sniff)
	ct := http.DetectContentType(sniff[:n])

	// Reconstruct the full stream: sniffed bytes prepended to the remainder.
	written, err := fh.h.ws.WriteFile(sessionID, userPath, io.MultiReader(bytes.NewReader(sniff[:n]), f), maxBytes)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now().UTC()

	fileMeta := &models.File{
		ID:          uuid.New(),
		SessionID:   sessionID,
		Path:        filepath.Clean("/" + userPath),
		SizeBytes:   written,
		ContentType: ct,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := dbpkg.UpsertFile(c.Request.Context(), fh.h.db, fileMeta); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist file metadata"})
		return
	}

	actor := middleware.Actor(c)
	fh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:     actor,
		SessionID: &sessionID,
		Action:    models.ActionFileUploaded,
		Metadata: models.JSONB{
			"path":       fileMeta.Path,
			"size_bytes": written,
		},
	})

	metrics.FilesUploadedTotal.Inc()
	metrics.FileBytesUploadedTotal.Add(float64(written))
	c.JSON(http.StatusCreated, fileMeta)
}

// GET /api/v1/sessions/:id/files
func (fh *FileHandler) List(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	// C-2: verify caller owns the session.
	if _, ok := fh.h.checkSessionAccess(c, sessionID); !ok {
		return
	}

	files, err := dbpkg.ListFiles(c.Request.Context(), fh.h.db, sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list files"})
		return
	}
	total, _ := dbpkg.CountFiles(c.Request.Context(), fh.h.db, sessionID)
	c.JSON(http.StatusOK, gin.H{"files": files, "total": total})
}

// GET /api/v1/sessions/:id/files/*path
func (fh *FileHandler) Download(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	userPath := c.Param("path")
	if userPath == "" || userPath == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}
	userPath = filepath.Clean("/" + userPath) // normalize for consistent policy eval

	// C-2: verify caller owns the session.
	if _, ok := fh.h.checkSessionAccess(c, sessionID); !ok {
		return
	}

	if d := fh.h.policy.EvalFileAccess(c.Request.Context(), sessionID, userPath, false); !d.Allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "file access denied by policy"})
		return
	}

	f, err := fh.h.ws.OpenFile(sessionID, userPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	defer f.Close()

	ct := detectContentType(userPath)

	// Sanitize filename for Content-Disposition to prevent header injection.
	// RFC 6266: only allow safe ASCII; strip control characters and quotes.
	safeName := sanitizeFilename(filepath.Base(userPath))
	c.Header("Content-Disposition", `attachment; filename="`+safeName+`"`)

	actor := middleware.Actor(c)
	fh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:     actor,
		SessionID: &sessionID,
		Action:    models.ActionFileDownloaded,
		Metadata:  models.JSONB{"path": userPath},
	})

	c.DataFromReader(http.StatusOK, -1, ct, f, nil)
}

// DELETE /api/v1/sessions/:id/files/*path
func (fh *FileHandler) Delete(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	userPath := c.Param("path")
	if userPath == "" || userPath == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}
	userPath = filepath.Clean("/" + userPath) // normalize for consistent policy eval

	// C-2: verify caller owns the session.
	if _, ok := fh.h.checkSessionAccess(c, sessionID); !ok {
		return
	}

	if d := fh.h.policy.EvalFileAccess(c.Request.Context(), sessionID, userPath, true); !d.Allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "file access denied by policy"})
		return
	}

	// Remove from filesystem (best-effort — may not exist if metadata got out of sync)
	if resolved, err := fh.h.ws.SafePath(sessionID, userPath); err == nil {
		_ = os.Remove(resolved)
	}

	if err := dbpkg.DeleteFile(c.Request.Context(), fh.h.db, sessionID, userPath); err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete file"})
		return
	}

	fh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:     middleware.Actor(c),
		SessionID: &sessionID,
		Action:    models.ActionFileDeleted,
		Metadata:  models.JSONB{"path": userPath},
	})

	c.Status(http.StatusNoContent)
}

// GET /api/v1/sessions/:id/workspace.zip
// Streams the entire workspace as a ZIP archive. Symlink escape protection is
// applied: symlinks whose real target lies outside the workspace are skipped.
func (fh *FileHandler) WorkspaceZip(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	// C-2: verify caller owns the session.
	if _, ok := fh.h.checkSessionAccess(c, sessionID); !ok {
		return
	}

	root := fh.h.ws.SessionPath(sessionID)

	// Verify the workspace directory exists before touching response headers.
	if _, err := os.Stat(root); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return
	}

	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition",
		fmt.Sprintf(`attachment; filename="workspace-%s.zip"`, sessionID))
	c.Header("Cache-Control", "no-store")

	zw := zip.NewWriter(c.Writer)
	defer zw.Close()

	rootWithSep := root + string(os.PathSeparator)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil // skip inaccessible entries
		}
		if info.IsDir() {
			return nil
		}

		// Resolve symlinks and reject escapes.
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil
		}
		if real != root && !strings.HasPrefix(real, rootWithSep) {
			slog.Warn("workspace zip: skipping symlink escape",
				"session_id", sessionID, "path", path, "target", real)
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		fw, err := zw.Create(rel)
		if err != nil {
			return err
		}

		f, err := os.Open(real)
		if err != nil {
			return nil // skip unreadable files
		}
		defer f.Close()

		_, err = io.Copy(fw, f)
		return err
	})
	if err != nil {
		// Headers already sent; log and let the client detect the truncated ZIP.
		slog.Error("workspace zip walk error", "session_id", sessionID, "err", err)
	}

	fh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:     middleware.Actor(c),
		SessionID: &sessionID,
		Action:    models.ActionFileDownloaded,
		Metadata:  models.JSONB{"path": "workspace.zip"},
	})
}

// sanitizeFilename strips control characters, quotes, and non-printable
// runes from a filename for safe use in HTTP headers.
func sanitizeFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '"' || r == '\\' || r == '\r' || r == '\n' || !unicode.IsPrint(r) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if s == "" {
		return "file"
	}
	return s
}

// detectContentType returns a MIME type for the given file path using the
// file extension. Used for Download responses where we are not trusting
// client input (no provided parameter accepted to prevent spoofing).
func detectContentType(path string) string {
	ext := filepath.Ext(path)
	if ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
	}
	return "application/octet-stream"
}

package handlers

import (
	"database/sql"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
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

	session, err := dbpkg.GetSession(c.Request.Context(), fh.h.db, sessionID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get session"})
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

	maxBytes := fh.h.cfg.Workspace.MaxFileMB * 1024 * 1024

	f, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open upload"})
		return
	}
	defer f.Close()

	written, err := fh.h.ws.WriteFile(sessionID, userPath, f, maxBytes)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ct := detectContentType(userPath, fileHeader.Header.Get("Content-Type"))
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

	c.JSON(http.StatusCreated, fileMeta)
}

// GET /api/v1/sessions/:id/files
func (fh *FileHandler) List(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	if _, err := dbpkg.GetSession(c.Request.Context(), fh.h.db, sessionID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	files, err := dbpkg.ListFiles(c.Request.Context(), fh.h.db, sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list files"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"files": files})
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

	// Verify session exists
	if _, err := dbpkg.GetSession(c.Request.Context(), fh.h.db, sessionID); err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	f, err := fh.h.ws.OpenFile(sessionID, userPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	defer f.Close()

	ct := detectContentType(userPath, "")

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

func detectContentType(path, provided string) string {
	if provided != "" && provided != "application/octet-stream" {
		return provided
	}
	ext := filepath.Ext(path)
	if ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
	}
	return "application/octet-stream"
}

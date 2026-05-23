package handlers

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
)

// SnapshotHandler serves snapshot endpoints under /sessions/:id/snapshots
// and the /snapshots/:id singleton routes.
type SnapshotHandler struct {
	h *Hub
}

func NewSnapshotHandler(h *Hub) *SnapshotHandler { return &SnapshotHandler{h: h} }

// POST /api/v1/sessions/:id/snapshots
// Body: {"name":"my-snapshot"}
// Creates a gzip-compressed tar archive of the session workspace and records
// it in the database. The archive path is never exposed to callers.
func (sh *SnapshotHandler) Create(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if _, ok := sh.h.checkSessionAccess(c, sessionID, models.OrgRoleExecutor); !ok {
		return
	}

	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	snapshotID := uuid.New()
	archivePath, sizeBytes, err := sh.h.ws.CreateSnapshot(sessionID, snapshotID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "snapshot creation failed: " + err.Error()})
		return
	}

	actor := middleware.Actor(c)
	snap := &models.Snapshot{
		ID:          snapshotID,
		SessionID:   sessionID,
		Name:        req.Name,
		CreatedBy:   actor,
		SizeBytes:   sizeBytes,
		ArchivePath: archivePath,
		CreatedAt:   time.Now().UTC(),
	}
	if err := dbpkg.CreateSnapshot(c.Request.Context(), sh.h.db, snap); err != nil {
		// Best-effort: remove the archive if DB insert fails.
		_ = sh.h.ws.DeleteSnapshot(archivePath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "persist snapshot failed"})
		return
	}

	sh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:     actor,
		SessionID: &sessionID,
		Action:    models.ActionSnapshotCreated,
		Metadata: models.JSONB{
			"snapshot_id": snapshotID.String(),
			"name":        req.Name,
			"size_bytes":  sizeBytes,
		},
	})

	c.JSON(http.StatusCreated, snap)
}

// GET /api/v1/sessions/:id/snapshots
func (sh *SnapshotHandler) List(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if _, ok := sh.h.checkSessionAccess(c, sessionID, models.OrgRoleViewer); !ok {
		return
	}

	snaps, err := dbpkg.ListSnapshots(c.Request.Context(), sh.h.db, sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list snapshots"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"snapshots": snaps, "total": len(snaps)})
}

// GET /api/v1/snapshots/:id/download
// Streams the snapshot archive as application/gzip.
func (sh *SnapshotHandler) Download(c *gin.Context) {
	snapID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	snap, err := dbpkg.GetSnapshot(c.Request.Context(), sh.h.db, snapID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "snapshot not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get snapshot"})
		return
	}

	// Verify the caller can access the source session.
	if _, ok := sh.h.checkSessionAccess(c, snap.SessionID, models.OrgRoleViewer); !ok {
		return
	}

	snapshotsBase := filepath.Join(sh.h.cfg.Workspace.BaseDir, "snapshots")
	if !strings.HasPrefix(filepath.Clean(snap.ArchivePath), snapshotsBase+string(os.PathSeparator)) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "snapshot path invalid"})
		return
	}

	filename := snap.Name + ".tar.gz"
	c.Header("Content-Disposition", `attachment; filename="`+sanitizeFilename(filename)+`"`)
	c.File(snap.ArchivePath)
}

// DELETE /api/v1/snapshots/:id
func (sh *SnapshotHandler) Delete(c *gin.Context) {
	snapID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	snap, err := dbpkg.GetSnapshot(c.Request.Context(), sh.h.db, snapID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "snapshot not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get snapshot"})
		return
	}

	if _, ok := sh.h.checkSessionAccess(c, snap.SessionID, models.OrgRoleExecutor); !ok {
		return
	}

	archivePath, err := dbpkg.DeleteSnapshot(c.Request.Context(), sh.h.db, snapID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "snapshot not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete snapshot"})
		return
	}
	_ = sh.h.ws.DeleteSnapshot(archivePath)

	actor := middleware.Actor(c)
	sh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:     actor,
		SessionID: &snap.SessionID,
		Action:    models.ActionSnapshotDeleted,
		Metadata:  models.JSONB{"snapshot_id": snapID.String()},
	})

	c.Status(http.StatusNoContent)
}

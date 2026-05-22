package handlers

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
)

// ArtifactHandler serves cross-session shared artifact endpoints.
type ArtifactHandler struct {
	h *Hub
}

func NewArtifactHandler(h *Hub) *ArtifactHandler { return &ArtifactHandler{h: h} }

// artifactDir returns the host directory where shared artifacts are stored.
func (ah *ArtifactHandler) artifactDir() string {
	return filepath.Join(ah.h.cfg.Workspace.BaseDir, "artifacts")
}

// POST /api/v1/sessions/:id/artifacts
// Promotes a file from the session workspace to the shared artifact registry.
// Body: {"path":"/workspace/output.csv","name":"output.csv"}  (name is optional)
func (ah *ArtifactHandler) Promote(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if _, ok := ah.h.checkSessionAccess(c, sessionID, models.OrgRoleViewer); !ok {
		return
	}

	actor := middleware.Actor(c)

	var req struct {
		Path string  `json:"path" binding:"required"`
		Name *string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Resolve and validate the source path inside the workspace.
	src, err := ah.h.ws.SafePath(sessionID, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path: " + err.Error()})
		return
	}

	info, err := os.Stat(src)
	if os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "source file not found"})
		return
	}
	if err != nil || !info.Mode().IsRegular() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path must point to a regular file"})
		return
	}

	// Enforce per-actor artifact storage quota (when configured).
	if maxMB := ah.h.cfg.Workspace.MaxArtifactStorageMB; maxMB > 0 && actor != "master" {
		used, err := dbpkg.TotalArtifactBytes(c.Request.Context(), ah.h.db, actor)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "quota check failed"})
			return
		}
		maxBytes := maxMB * 1024 * 1024
		if used+info.Size() > maxBytes {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": fmt.Sprintf("artifact storage quota of %d MB exceeded", maxMB),
			})
			return
		}
	}

	// Determine the artifact name (fall back to the base filename).
	name := filepath.Base(req.Path)
	if req.Name != nil && *req.Name != "" {
		name = *req.Name
	}
	name = sanitizeFilename(name)

	// Detect content type from the first 512 bytes.
	ff, err := os.Open(src)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "open source file failed"})
		return
	}
	sniff := make([]byte, 512)
	n, _ := ff.Read(sniff)
	ct := http.DetectContentType(sniff[:n])
	_, _ = ff.Seek(0, io.SeekStart)

	// Copy into the artifact store.
	artifactID := uuid.New()
	if err := os.MkdirAll(ah.artifactDir(), 0o700); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "artifact dir unavailable"})
		return
	}
	destPath := filepath.Join(ah.artifactDir(), artifactID.String())
	dest, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create artifact file failed"})
		return
	}
	sizeBytes, copyErr := io.Copy(dest, ff)
	_ = dest.Close()
	_ = ff.Close()
	if copyErr != nil {
		_ = os.Remove(destPath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "copy artifact failed"})
		return
	}

	art := &models.SharedArtifact{
		ID:           artifactID,
		Name:         name,
		ArtifactPath: destPath,
		SizeBytes:    sizeBytes,
		ContentType:  ct,
		CreatedBy:    actor,
		SessionID:    &sessionID,
		CreatedAt:    time.Now().UTC(),
	}
	if err := dbpkg.CreateArtifact(c.Request.Context(), ah.h.db, art); err != nil {
		_ = os.Remove(destPath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "persist artifact failed"})
		return
	}

	ah.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:     actor,
		SessionID: &sessionID,
		Action:    models.ActionArtifactCreated,
		Metadata: models.JSONB{
			"artifact_id": artifactID.String(),
			"name":        name,
			"size_bytes":  sizeBytes,
			"source_path": req.Path,
		},
	})

	c.JSON(http.StatusCreated, art)
}

// GET /api/v1/artifacts
// Lists shared artifacts. Non-master callers see only their own artifacts.
func (ah *ArtifactHandler) List(c *gin.Context) {
	pg := pagination(c)
	actor := middleware.Actor(c)
	listActor := actor
	if listActor == "master" {
		listActor = ""
	}

	arts, err := dbpkg.ListArtifacts(c.Request.Context(), ah.h.db, listActor, pg.limit, pg.offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list artifacts"})
		return
	}
	total, _ := dbpkg.CountArtifacts(c.Request.Context(), ah.h.db, listActor)
	c.JSON(http.StatusOK, gin.H{"artifacts": arts, "pagination": pg.response(total)})
}

// GET /api/v1/artifacts/:id/download
// Streams the artifact file. Non-master callers may only download artifacts
// they created.
func (ah *ArtifactHandler) Download(c *gin.Context) {
	artID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	art, err := dbpkg.GetArtifact(c.Request.Context(), ah.h.db, artID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "artifact not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get artifact"})
		return
	}

	actor := middleware.Actor(c)
	if actor != "master" && art.CreatedBy != actor {
		c.JSON(http.StatusNotFound, gin.H{"error": "artifact not found"})
		return
	}

	c.Header("Content-Disposition", `attachment; filename="`+sanitizeFilename(art.Name)+`"`)
	c.File(art.ArtifactPath)
}

// DELETE /api/v1/artifacts/:id
func (ah *ArtifactHandler) Delete(c *gin.Context) {
	artID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	art, err := dbpkg.GetArtifact(c.Request.Context(), ah.h.db, artID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "artifact not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get artifact"})
		return
	}

	actor := middleware.Actor(c)
	if actor != "master" && art.CreatedBy != actor {
		c.JSON(http.StatusNotFound, gin.H{"error": "artifact not found"})
		return
	}

	artifactPath, err := dbpkg.DeleteArtifact(c.Request.Context(), ah.h.db, artID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "artifact not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete artifact"})
		return
	}
	_ = os.Remove(artifactPath)

	ah.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:  actor,
		Action: models.ActionArtifactDeleted,
		Metadata: models.JSONB{
			"artifact_id": artID.String(),
			"name":        art.Name,
		},
	})

	c.Status(http.StatusNoContent)
}

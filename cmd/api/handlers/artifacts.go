package handlers

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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

// POST /api/v1/sessions/:id/artifacts
// Promotes a file from the session workspace to the shared artifact registry.
// Body: {"path":"/workspace/output.csv","name":"output.csv"}  (name is optional)
func (ah *ArtifactHandler) Promote(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if _, ok := ah.h.checkSessionAccess(c, sessionID, models.OrgRoleExecutor); !ok {
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
	defer ff.Close()
	sniff := make([]byte, 512)
	n, _ := ff.Read(sniff)
	ct := http.DetectContentType(sniff[:n])
	if _, err := ff.Seek(0, io.SeekStart); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "seek source file failed"})
		return
	}

	// Store artifact — the key is the UUID string, handled by the configured store.
	artifactID := uuid.New()
	if err := ah.h.artifactStore.Put(c.Request.Context(), artifactID.String(), ff, info.Size(), ct); err != nil {
		slog.Error("artifact: store put failed", "session_id", sessionID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "store artifact failed"})
		return
	}

	art := &models.SharedArtifact{
		ID:           artifactID,
		Name:         name,
		ArtifactPath: artifactID.String(), // storage key (UUID)
		SizeBytes:    info.Size(),
		ContentType:  ct,
		CreatedBy:    actor,
		SessionID:    &sessionID,
		CreatedAt:    time.Now().UTC(),
	}
	if err := dbpkg.CreateArtifact(c.Request.Context(), ah.h.db, art); err != nil {
		// Best-effort: remove the stored blob if DB insert fails.
		_ = ah.h.artifactStore.Delete(c.Request.Context(), artifactID.String())
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
			"size_bytes":  info.Size(),
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

	rc, err := ah.h.artifactStore.Get(c.Request.Context(), art.ArtifactPath)
	if err != nil {
		slog.Error("artifact: store get failed", "artifact_id", artID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read artifact"})
		return
	}
	defer rc.Close()

	c.Header("Content-Disposition", `attachment; filename="`+sanitizeFilename(art.Name)+`"`)
	c.Header("Content-Type", art.ContentType)
	c.Header("Content-Length", strconv.FormatInt(art.SizeBytes, 10))
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, rc); err != nil {
		slog.Warn("artifact: stream interrupted", "artifact_id", artID, "err", err)
	}
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

	artifactKey, err := dbpkg.DeleteArtifact(c.Request.Context(), ah.h.db, artID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "artifact not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete artifact"})
		return
	}
	if err := ah.h.artifactStore.Delete(c.Request.Context(), artifactKey); err != nil {
		slog.Warn("artifact: store delete failed", "artifact_id", artID, "err", err)
	}

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

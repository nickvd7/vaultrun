package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// DockerHandler serves Docker image and container-management endpoints.
// All routes are restricted to the master API key except the per-session
// stats/logs endpoints which use the standard session-access check.
type DockerHandler struct{ h *Hub }

func NewDockerHandler(h *Hub) *DockerHandler { return &DockerHandler{h: h} }

// GET /api/v1/docker/images
// Returns the list of Docker images locally available on the host.
func (dh *DockerHandler) ListImages(c *gin.Context) {
	if dh.h.docker == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Docker not available"})
		return
	}
	imgs, err := dh.h.docker.ListImages(c.Request.Context())
	if err != nil {
		slog.Error("docker: list images", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list images"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"images": imgs, "total": len(imgs)})
}

// POST /api/v1/docker/images/pull
// Body: {"image":"python:3.12-slim"}
// Pulls a Docker image from the registry and blocks until complete.
func (dh *DockerHandler) PullImage(c *gin.Context) {
	if dh.h.docker == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Docker not available"})
		return
	}
	var req struct {
		Image string `json:"image" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := dh.h.docker.PullImage(c.Request.Context(), req.Image); err != nil {
		slog.Error("docker: pull image", "image", req.Image, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "image pull failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "pulled", "image": req.Image})
}

// GET /api/v1/sessions/:id/stats
// Returns a one-shot CPU/memory/network snapshot for the session's container.
func (dh *DockerHandler) SessionStats(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if _, ok := dh.h.checkSessionAccess(c, sessionID, "viewer"); !ok {
		return
	}
	if dh.h.docker == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Docker not available"})
		return
	}
	containerName := fmt.Sprintf("%s-%s", dh.h.cfg.Docker.ContainerPrefix, sessionID.String())
	stats, err := dh.h.docker.ContainerStats(c.Request.Context(), containerName)
	if err != nil {
		slog.Error("docker: container stats", "session", sessionID.String(), "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get container stats"})
		return
	}
	c.JSON(http.StatusOK, stats)
}

// GET /api/v1/sessions/:id/logs?tail=100
// Streams the last N lines of stdout+stderr from the session's container.
func (dh *DockerHandler) SessionLogs(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if _, ok := dh.h.checkSessionAccess(c, sessionID, "viewer"); !ok {
		return
	}
	if dh.h.docker == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Docker not available"})
		return
	}

	tail := 100
	if v := c.Query("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 10000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "tail must be between 1 and 10000"})
			return
		}
		tail = n
	}

	containerName := fmt.Sprintf("%s-%s", dh.h.cfg.Docker.ContainerPrefix, sessionID.String())
	logs, err := dh.h.docker.ContainerLogs(c.Request.Context(), containerName, tail)
	if err != nil {
		slog.Error("docker: container logs", "session", sessionID.String(), "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get container logs"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"logs": logs})
}

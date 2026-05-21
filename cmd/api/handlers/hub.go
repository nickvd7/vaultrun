package handlers

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/config"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/jobqueue"
	"github.com/nickvd7/vaultrun/internal/models"
	"github.com/nickvd7/vaultrun/internal/policy"
	"github.com/nickvd7/vaultrun/internal/runner"
	"github.com/nickvd7/vaultrun/internal/workspace"
)

// Hub holds shared dependencies that all handlers need.
type Hub struct {
	db     *sqlx.DB
	docker *dockerpkg.Client
	ws     *workspace.Manager
	runner *runner.Runner
	audit  *audit.Logger
	cfg    *config.Config
	policy policy.Hook
	queue  *jobqueue.Queue
}

func NewHub(
	db *sqlx.DB,
	docker *dockerpkg.Client,
	ws *workspace.Manager,
	runner *runner.Runner,
	audit *audit.Logger,
	cfg *config.Config,
	pol policy.Hook,
	queue *jobqueue.Queue,
) *Hub {
	if pol == nil {
		pol = policy.AllowAll{}
	}
	return &Hub{db: db, docker: docker, ws: ws, runner: runner, audit: audit, cfg: cfg, policy: pol, queue: queue}
}

// Policy exposes the active policy hook (for the policy handler).
func (h *Hub) Policy() policy.Hook { return h.policy }

// PolicyFile returns the configured OPA policy file path (empty = AllowAll).
func (h *Hub) PolicyFile() string { return h.cfg.Auth.OPAPolicyFile }

type paginationParams struct {
	limit  int
	offset int
	page   int
}

func pagination(c *gin.Context) paginationParams {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * limit
	return paginationParams{limit: limit, offset: offset, page: page}
}

func parseUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + param})
		return uuid.UUID{}, false
	}
	return id, true
}

// checkSessionAccess loads a session and verifies the caller owns it (C-2).
// The master actor bypasses the ownership check and sees all sessions.
// On failure the handler writes the appropriate JSON response and returns nil.
// Pattern: session, ok := h.checkSessionAccess(c, id); if !ok { return }
func (h *Hub) checkSessionAccess(c *gin.Context, sessionID uuid.UUID) (*models.Session, bool) {
	session, err := dbpkg.GetSession(c.Request.Context(), h.db, sessionID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return nil, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get session"})
		return nil, false
	}
	actor := middleware.Actor(c)
	// Non-master callers may only access their own sessions. Return 404 (not
	// 403) to avoid leaking the existence of sessions owned by other actors.
	if actor != "master" && session.CreatedBy != actor {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return nil, false
	}
	return session, true
}

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
	"github.com/nickvd7/vaultrun/internal/secrets"
	"github.com/nickvd7/vaultrun/internal/warmpool"
	"github.com/nickvd7/vaultrun/internal/workspace"
)

// Hub holds shared dependencies that all handlers need.
type Hub struct {
	db       *sqlx.DB
	docker   *dockerpkg.Client
	ws       *workspace.Manager
	runner   *runner.Runner
	audit    *audit.Logger
	cfg      *config.Config
	policy   policy.Hook
	queue    jobqueue.Queue // interface — nil when async not configured
	secrets  secrets.Provider
	warmPool *warmpool.Pool // nil when pool disabled
}

func NewHub(
	db *sqlx.DB,
	docker *dockerpkg.Client,
	ws *workspace.Manager,
	runner *runner.Runner,
	audit *audit.Logger,
	cfg *config.Config,
	pol policy.Hook,
	queue jobqueue.Queue,
	sec secrets.Provider,
	pool *warmpool.Pool,
) *Hub {
	if pol == nil {
		pol = policy.AllowAll{}
	}
	return &Hub{db: db, docker: docker, ws: ws, runner: runner, audit: audit, cfg: cfg, policy: pol, queue: queue, secrets: sec, warmPool: pool}
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

// response builds the JSON-serialisable pagination envelope with a total count.
// Usage: gin.H{"sessions": sessions, "pagination": pg.response(total)}
func (p paginationParams) response(total int) gin.H {
	return gin.H{
		"page":   p.page,
		"limit":  p.limit,
		"offset": p.offset,
		"total":  total,
	}
}

func parseUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + param})
		return uuid.UUID{}, false
	}
	return id, true
}

// checkSessionAccess loads a session and verifies the caller has at least
// minRole access to it (C-2 tenant isolation + v0.3 org RBAC).
//
// Access is granted when any of the following is true:
//  1. actor is "master" (always full access)
//  2. actor is the session owner (full access)
//  3. the session belongs to an org and the actor is a member with
//     role ≥ minRole
//
// On failure, the handler writes the appropriate JSON response (always 404
// to avoid leaking session existence to unauthorized callers) and returns nil.
//
// Pattern: session, ok := h.checkSessionAccess(c, id, models.OrgRoleViewer)
func (h *Hub) checkSessionAccess(c *gin.Context, sessionID uuid.UUID, minRole string) (*models.Session, bool) {
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
	// Master key sees everything.
	if actor == "master" {
		return session, true
	}
	// Session owner always has full access regardless of role.
	if session.CreatedBy == actor {
		return session, true
	}
	// Org membership check: actor must be a member with sufficient role.
	if session.OrgID != nil {
		role, err := dbpkg.GetOrgMemberRole(c.Request.Context(), h.db, *session.OrgID, actor)
		if err == nil && models.RoleAtLeast(role, minRole) {
			return session, true
		}
	}
	// Always return 404 (never 403) to avoid leaking session existence.
	c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
	return nil, false
}

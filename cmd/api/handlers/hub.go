package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/config"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
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
}

func NewHub(
	db *sqlx.DB,
	docker *dockerpkg.Client,
	ws *workspace.Manager,
	runner *runner.Runner,
	audit *audit.Logger,
	cfg *config.Config,
	pol policy.Hook,
) *Hub {
	if pol == nil {
		pol = policy.AllowAll{}
	}
	return &Hub{db: db, docker: docker, ws: ws, runner: runner, audit: audit, cfg: cfg, policy: pol}
}

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

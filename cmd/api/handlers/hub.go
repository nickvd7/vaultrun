package handlers

import (
	"context"
	"crypto/x509"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/artifacts"
	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/config"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/evidence"
	"github.com/nickvd7/vaultrun/internal/jobqueue"
	"github.com/nickvd7/vaultrun/internal/models"
	"github.com/nickvd7/vaultrun/internal/policy"
	"github.com/nickvd7/vaultrun/internal/quota"
	"github.com/nickvd7/vaultrun/internal/runner"
	"github.com/nickvd7/vaultrun/internal/secrets"
	"github.com/nickvd7/vaultrun/internal/warmpool"
	"github.com/nickvd7/vaultrun/internal/workspace"
)

// Hub holds shared dependencies that all handlers need.
type Hub struct {
	db             *sqlx.DB
	docker         *dockerpkg.Client
	ws             *workspace.Manager
	runner         *runner.Runner
	quota          *quota.Enforcer // nil when MAX_WORKSPACE_MB is 0
	xfsQuota       *quota.XFS      // nil unless WORKSPACE_QUOTA=xfs
	audit          *audit.Logger
	cfg            *config.Config
	policy         policy.Hook
	queue          jobqueue.Queue // interface — nil when async not configured
	secrets        secrets.Provider
	warmPool       *warmpool.Pool // nil when pool disabled
	artifactStore  artifacts.Store
	evidenceSigner *evidence.Signer // nil when EVIDENCE_SIGNING_KEY is unset
	// EVIDENCE_TSA_URL / EVIDENCE_TSA_CA_FILE; empty URL = no timestamps.
	evidenceTSAURL   string
	evidenceTSARoots *x509.CertPool
	sessionManager   *sessionManager // session access helper
	orgManager       *orgManager     // org access helper
}

// sessionManager provides session access helpers
type sessionManager struct {
	db *sqlx.DB
}

func (sm *sessionManager) GetSession(ctx context.Context, id uuid.UUID) (*models.Session, error) {
	return dbpkg.GetSession(ctx, sm.db, id)
}

// orgManager provides org access helpers
type orgManager struct {
	db *sqlx.DB
}

func (om *orgManager) GetUserOrgRole(ctx context.Context, principal string, orgID uuid.UUID) (*models.OrgMember, error) {
	role, err := dbpkg.GetOrgMemberRole(ctx, om.db, orgID, principal)
	if err != nil {
		return nil, err
	}
	return &models.OrgMember{
		Role: role,
	}, nil
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
	artStore artifacts.Store,
) *Hub {
	if pol == nil {
		pol = policy.AllowAll{}
	}
	if artStore == nil {
		artStore = artifacts.NewLocalStore(cfg.Workspace.BaseDir + "/artifacts")
	}
	return &Hub{
		db: db, docker: docker, ws: ws, runner: runner, audit: audit,
		cfg: cfg, policy: pol, queue: queue, secrets: sec, warmPool: pool,
		artifactStore:  artStore,
		sessionManager: &sessionManager{db: db},
		orgManager:     &orgManager{db: db},
	}
}

// SetEvidenceSigner enables session evidence export.
func (h *Hub) SetEvidenceSigner(s *evidence.Signer) { h.evidenceSigner = s }

// SetXFSQuota makes every new workspace get a hard XFS project quota.
func (h *Hub) SetXFSQuota(x *quota.XFS) { h.xfsQuota = x }

// SetQuota makes runs check the workspace size cap first.
func (h *Hub) SetQuota(q *quota.Enforcer) { h.quota = q }

// admitRun refuses a run while the session's workspace is over its cap (and
// unpauses a sandbox the cap had paused once it is back under). It writes the
// response and returns false when the run must not start.
func (h *Hub) admitRun(c *gin.Context, s *models.Session) bool {
	err := h.quota.Admit(c.Request.Context(), s)
	if err == nil {
		return true
	}
	if errors.Is(err, quota.ErrOverQuota) {
		c.JSON(http.StatusInsufficientStorage, gin.H{"error": err.Error()})
		return false
	}
	slog.Error("quota admission failed", "session_id", s.ID, "err", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resume the sandbox"})
	return false
}

// SetEvidenceTSA makes every exported pack carry an RFC 3161 timestamp from
// url, whose signer must chain to roots (nil = system roots).
func (h *Hub) SetEvidenceTSA(url string, roots *x509.CertPool) {
	h.evidenceTSAURL, h.evidenceTSARoots = url, roots
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
	if page > 1_000_000 {
		page = 1_000_000
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
	session, err := h.sessionIfAccessible(c, sessionID, minRole)
	if err == errSessionAccessDenied {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return nil, false
	}
	if err != nil {
		slog.Error("failed to get session", "err", err, "method", c.Request.Method, "path", c.FullPath())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get session"})
		return nil, false
	}
	return session, true
}

// errSessionAccessDenied is returned by sessionIfAccessible when the session
// is missing or the caller lacks minRole (callers map this to a uniform 404).
var errSessionAccessDenied = errors.New("session access denied")

// sessionIfAccessible returns the session when the caller may access it.
// Does not write an HTTP response — use when the caller needs a custom error body.
func (h *Hub) sessionIfAccessible(c *gin.Context, sessionID uuid.UUID, minRole string) (*models.Session, error) {
	session, err := dbpkg.GetSession(c.Request.Context(), h.db, sessionID)
	if err == sql.ErrNoRows {
		return nil, errSessionAccessDenied
	}
	if err != nil {
		return nil, err
	}
	actor := middleware.Actor(c)
	if actor == "master" {
		return session, nil
	}
	if session.CreatedBy == actor {
		return session, nil
	}
	if session.OrgID != nil {
		role, err := dbpkg.GetOrgMemberRole(c.Request.Context(), h.db, *session.OrgID, actor)
		if err == nil && models.RoleAtLeast(role, minRole) {
			return session, nil
		}
	}
	return nil, errSessionAccessDenied
}

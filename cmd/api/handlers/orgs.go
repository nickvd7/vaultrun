package handlers

import (
	"database/sql"
	"net/http"
	"regexp"
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

// OrgHandler serves /api/v1/orgs and /api/v1/orgs/:id/members.
type OrgHandler struct {
	h *Hub
}

func NewOrgHandler(h *Hub) *OrgHandler { return &OrgHandler{h: h} }

var slugRE = regexp.MustCompile(`[^a-z0-9-]+`)

// slugify converts a name to a URL-safe slug: lowercase, spaces/specials → dash,
// leading/trailing dashes stripped, runs of dashes collapsed.
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	// Replace non-alnum characters with a dash.
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	s = slugRE.ReplaceAllString(b.String(), "-")
	return strings.Trim(s, "-")
}

// ─── Organization CRUD ────────────────────────────────────────────────────────

type createOrgRequest struct {
	Name string  `json:"name" binding:"required"`
	Slug *string `json:"slug"` // auto-generated from name if omitted
}

// POST /api/v1/orgs  (master key only)
func (oh *OrgHandler) Create(c *gin.Context) {
	var req createOrgRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	slug := ""
	if req.Slug != nil && *req.Slug != "" {
		slug = slugify(*req.Slug)
	} else {
		slug = slugify(req.Name)
	}
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name produces an empty slug"})
		return
	}

	// Reject duplicate slugs with a friendly error before hitting the DB constraint.
	if existing, _ := dbpkg.GetOrgBySlug(c.Request.Context(), oh.h.db, slug); existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "slug already taken"})
		return
	}

	now := time.Now().UTC()
	org := &models.Organization{
		ID:        uuid.New(),
		Name:      req.Name,
		Slug:      slug,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := dbpkg.CreateOrg(c.Request.Context(), oh.h.db, org); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create org"})
		return
	}

	oh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:  middleware.Actor(c),
		Action: models.ActionOrgCreated,
		Metadata: models.JSONB{
			"org_id": org.ID.String(),
			"slug":   org.Slug,
		},
	})

	c.JSON(http.StatusCreated, org)
}

// GET /api/v1/orgs  (master key only)
func (oh *OrgHandler) List(c *gin.Context) {
	pg := pagination(c)
	orgs, err := dbpkg.ListOrgs(c.Request.Context(), oh.h.db, pg.limit, pg.offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list orgs"})
		return
	}
	total, _ := dbpkg.CountOrgs(c.Request.Context(), oh.h.db)
	c.JSON(http.StatusOK, gin.H{"orgs": orgs, "pagination": pg.response(total)})
}

// GET /api/v1/orgs/:id  (master key or any org member)
func (oh *OrgHandler) Get(c *gin.Context) {
	orgID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	org, ok := oh.requireOrgAccess(c, orgID, "")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, org)
}

// DELETE /api/v1/orgs/:id  (master key only)
func (oh *OrgHandler) Delete(c *gin.Context) {
	orgID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := dbpkg.DeleteOrg(c.Request.Context(), oh.h.db, orgID); err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "org not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete org"})
		return
	}

	oh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:  middleware.Actor(c),
		Action: models.ActionOrgDeleted,
		Metadata: models.JSONB{
			"org_id": orgID.String(),
		},
	})

	c.Status(http.StatusNoContent)
}

// ─── Org members ─────────────────────────────────────────────────────────────

type addMemberRequest struct {
	Principal string `json:"principal" binding:"required"` // API key name (actor)
	Role      string `json:"role"`                         // viewer | executor | admin; default executor
}

// POST /api/v1/orgs/:id/members  (master key or org admin)
func (oh *OrgHandler) AddMember(c *gin.Context) {
	orgID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if _, ok := oh.requireOrgAccess(c, orgID, models.OrgRoleAdmin); !ok {
		return
	}

	var req addMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	role := req.Role
	if role == "" {
		role = models.OrgRoleExecutor
	}
	if role != models.OrgRoleViewer && role != models.OrgRoleExecutor && role != models.OrgRoleAdmin {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role must be viewer, executor, or admin"})
		return
	}

	member := &models.OrgMember{
		OrgID:     orgID,
		Principal: req.Principal,
		Role:      role,
		CreatedAt: time.Now().UTC(),
	}
	if err := dbpkg.AddOrgMember(c.Request.Context(), oh.h.db, member); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add member"})
		return
	}

	oh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:  middleware.Actor(c),
		Action: models.ActionOrgMemberAdded,
		Metadata: models.JSONB{
			"org_id":    orgID.String(),
			"principal": req.Principal,
			"role":      role,
		},
	})

	c.JSON(http.StatusCreated, member)
}

// GET /api/v1/orgs/:id/members  (master key or any org member)
func (oh *OrgHandler) ListMembers(c *gin.Context) {
	orgID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if _, ok := oh.requireOrgAccess(c, orgID, models.OrgRoleViewer); !ok {
		return
	}

	members, err := dbpkg.ListOrgMembers(c.Request.Context(), oh.h.db, orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list members"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"members": members, "total": len(members)})
}

// DELETE /api/v1/orgs/:id/members/:principal  (master key or org admin)
func (oh *OrgHandler) RemoveMember(c *gin.Context) {
	orgID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if _, ok := oh.requireOrgAccess(c, orgID, models.OrgRoleAdmin); !ok {
		return
	}

	principal := c.Param("principal")
	if principal == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "principal required"})
		return
	}

	if err := dbpkg.RemoveOrgMember(c.Request.Context(), oh.h.db, orgID, principal); err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "member not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove member"})
		return
	}

	oh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:  middleware.Actor(c),
		Action: models.ActionOrgMemberRemoved,
		Metadata: models.JSONB{
			"org_id":    orgID.String(),
			"principal": principal,
		},
	})

	c.Status(http.StatusNoContent)
}

// GET /api/v1/orgs/:id/sessions  (org member — respects role visibility)
func (oh *OrgHandler) ListSessions(c *gin.Context) {
	orgID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if _, ok := oh.requireOrgAccess(c, orgID, models.OrgRoleViewer); !ok {
		return
	}

	pg := pagination(c)
	sessions, err := dbpkg.ListOrgSessions(c.Request.Context(), oh.h.db, orgID, pg.limit, pg.offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list org sessions"})
		return
	}
	total, _ := dbpkg.CountOrgSessions(c.Request.Context(), oh.h.db, orgID)
	c.JSON(http.StatusOK, gin.H{"sessions": sessions, "pagination": pg.response(total)})
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

// requireOrgAccess loads the org and verifies the caller has at least minRole
// access. Master key always passes. An empty minRole skips the role check (any
// member may access). Returns 404 for unknown orgs (avoids existence leakage).
func (oh *OrgHandler) requireOrgAccess(c *gin.Context, orgID uuid.UUID, minRole string) (*models.Organization, bool) {
	org, err := dbpkg.GetOrg(c.Request.Context(), oh.h.db, orgID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "org not found"})
		return nil, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get org"})
		return nil, false
	}

	actor := middleware.Actor(c)
	if actor == "master" {
		return org, true
	}

	role, err := dbpkg.GetOrgMemberRole(c.Request.Context(), oh.h.db, orgID, actor)
	if err != nil {
		// Not a member — 404 to avoid leaking org existence.
		c.JSON(http.StatusNotFound, gin.H{"error": "org not found"})
		return nil, false
	}
	if minRole != "" && !models.RoleAtLeast(role, minRole) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient org role"})
		return nil, false
	}

	return org, true
}

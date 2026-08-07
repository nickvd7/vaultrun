package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/missions"
)

type MissionHandler struct {
	manager *missions.Manager
	hub     *Hub
}

func NewMissionHandler(manager *missions.Manager, hub *Hub) *MissionHandler {
	return &MissionHandler{manager: manager, hub: hub}
}

func (h *MissionHandler) callerOrg(c *gin.Context, actor string) (*uuid.UUID, error) {
	if actor == "master" {
		return nil, nil
	}
	var orgID uuid.UUID
	err := h.hub.db.GetContext(c.Request.Context(), &orgID,
		"SELECT org_id FROM org_members WHERE principal = $1 ORDER BY created_at LIMIT 1", actor)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &orgID, nil
}

func (h *MissionHandler) mayRead(c *gin.Context, actor string, m *missions.Mission) bool {
	if actor == "master" || m.Published {
		return true
	}
	if m.CreatedBy != "" && m.CreatedBy == actor {
		return true
	}
	if m.OrgID == nil {
		return false
	}
	_, err := dbpkg.GetOrgMemberRole(c.Request.Context(), h.hub.db, *m.OrgID, actor)
	return err == nil
}

func (h *MissionHandler) mayWrite(c *gin.Context, actor string, m *missions.Mission) bool {
	if actor == "master" {
		return true
	}
	if m.CreatedBy == actor {
		return true
	}
	if m.OrgID == nil {
		return false
	}
	role, err := dbpkg.GetOrgMemberRole(c.Request.Context(), h.hub.db, *m.OrgID, actor)
	return err == nil && (role == "admin" || role == "executor")
}

func (h *MissionHandler) List(c *gin.Context) {
	var filter missions.MissionFilter
	_ = c.ShouldBindQuery(&filter)
	// Non-master callers only see published unless they filter otherwise — default published=true for anon-ish keys.
	actor := middleware.Actor(c)
	if actor != "master" && filter.Published == nil {
		pub := true
		filter.Published = &pub
	}
	list, err := h.manager.List(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list missions failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"missions": list})
}

func (h *MissionHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	m, err := h.manager.Get(c.Request.Context(), id)
	if err == missions.ErrMissionNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "get failed"})
		return
	}
	actor := middleware.Actor(c)
	if !h.mayRead(c, actor, m) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, m)
}

func (h *MissionHandler) GetBySlug(c *gin.Context) {
	m, err := h.manager.GetBySlug(c.Request.Context(), c.Param("slug"))
	if err == missions.ErrMissionNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "get failed"})
		return
	}
	actor := middleware.Actor(c)
	if !h.mayRead(c, actor, m) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, m)
}

func (h *MissionHandler) Create(c *gin.Context) {
	var req missions.CreateMissionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	actor := middleware.Actor(c)
	orgID, err := h.callerOrg(c, actor)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "org lookup failed"})
		return
	}
	m, err := h.manager.Create(c.Request.Context(), orgID, actor, req)
	if err == missions.ErrMissionSlugExists {
		c.JSON(http.StatusConflict, gin.H{"error": "slug exists"})
		return
	}
	if errors.Is(err, missions.ErrInvalidMission) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create failed"})
		return
	}
	c.JSON(http.StatusCreated, m)
}

func (h *MissionHandler) Update(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	cur, err := h.manager.Get(c.Request.Context(), id)
	if err == missions.ErrMissionNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "get failed"})
		return
	}
	actor := middleware.Actor(c)
	if !h.mayWrite(c, actor, cur) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	var req missions.UpdateMissionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m, err := h.manager.Update(c.Request.Context(), id, req)
	if errors.Is(err, missions.ErrInvalidMission) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed"})
		return
	}
	c.JSON(http.StatusOK, m)
}

func (h *MissionHandler) Delete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	cur, err := h.manager.Get(c.Request.Context(), id)
	if err == missions.ErrMissionNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "get failed"})
		return
	}
	actor := middleware.Actor(c)
	if !h.mayWrite(c, actor, cur) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if err := h.manager.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "delete failed"})
		return
	}
	c.Status(http.StatusNoContent)
}

// RecordRun attaches a mission execution record to a session (does not execute steps server-side yet).
func (h *MissionHandler) RecordRun(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	cur, err := h.manager.Get(c.Request.Context(), id)
	if err == missions.ErrMissionNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "get failed"})
		return
	}
	actor := middleware.Actor(c)
	if !h.mayRead(c, actor, cur) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var req missions.StartMissionRunRequest
	_ = c.ShouldBindJSON(&req)
	orgID, _ := h.callerOrg(c, actor)
	run, err := h.manager.RecordRun(c.Request.Context(), id, req.SessionID, orgID, "recorded", []any{}, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "record failed"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"run": run, "mission": cur, "hint": "Execute steps via MCP tools; call verify endpoints per step when available."})
}

func (h *MissionHandler) ListRuns(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	cur, err := h.manager.Get(c.Request.Context(), id)
	if err == missions.ErrMissionNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "get failed"})
		return
	}
	actor := middleware.Actor(c)
	if !h.mayRead(c, actor, cur) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	runs, err := h.manager.ListRuns(c.Request.Context(), id, 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list runs failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"runs": runs})
}

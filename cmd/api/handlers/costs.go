package handlers

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/cost"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
)

type CostHandler struct {
	h       *Hub
	tracker *cost.Tracker
}

func NewCostHandler(h *Hub, tracker *cost.Tracker) *CostHandler {
	return &CostHandler{h: h, tracker: tracker}
}

// GET /api/v1/sessions/:id/costs
func (ch *CostHandler) GetSessionCosts(c *gin.Context) {
	sessionID, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	// Check session access
	if _, ok := ch.h.checkSessionAccess(c, sessionID, models.OrgRoleViewer); !ok {
		return
	}

	metrics, err := ch.tracker.GetSessionCosts(c.Request.Context(), sessionID)
	if err != nil {
		slog.Error("get session costs", "err", err, "session_id", sessionID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get costs"})
		return
	}

	summary, err := ch.tracker.GetSessionSummary(c.Request.Context(), sessionID)
	if err != nil {
		slog.Error("get session summary", "err", err, "session_id", sessionID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get summary"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"metrics": metrics,
		"summary": summary,
	})
}

// GET /api/v1/costs/breakdown?period=YYYY-MM
func (ch *CostHandler) GetCostBreakdown(c *gin.Context) {
	period := c.DefaultQuery("period", time.Now().Format("2006-01"))

	// Validate period format
	if _, err := time.Parse("2006-01", period); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid period format, use YYYY-MM"})
		return
	}

	breakdown, err := ch.tracker.GetCostBreakdown(c.Request.Context(), period)
	if err != nil {
		slog.Error("get cost breakdown", "err", err, "period", period)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get breakdown"})
		return
	}

	c.JSON(http.StatusOK, breakdown)
}

// GET /api/v1/orgs/:id/costs?month=YYYY-MM
func (ch *CostHandler) GetOrgCosts(c *gin.Context) {
	orgID, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	month := c.DefaultQuery("month", time.Now().Format("2006-01"))

	// Verify org access
	actor := middleware.Actor(c)
	if actor != "master" {
		role, err := dbpkg.GetOrgMemberRole(c.Request.Context(), ch.h.db, orgID, actor)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
			return
		}
		if !models.RoleAtLeast(role, models.OrgRoleViewer) {
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
			return
		}
	}

	summary, err := ch.tracker.GetOrgSummary(c.Request.Context(), orgID, month)
	if err != nil {
		slog.Error("get org costs", "err", err, "org_id", orgID, "month", month)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get org costs"})
		return
	}

	// Get budget if exists
	var budget *cost.CostBudget
	err = ch.h.db.GetContext(c.Request.Context(), &budget, `
		SELECT * FROM cost_budgets 
		WHERE org_id = $1 AND current_month = $2
	`, orgID, month)
	if err != nil && err != sql.ErrNoRows {
		slog.Error("get budget", "err", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"summary": summary,
		"budget":  budget,
	})
}

// GET /api/v1/costs/alerts
func (ch *CostHandler) GetAlerts(c *gin.Context) {
	resolved := c.DefaultQuery("resolved", "false") == "true"

	alerts, err := ch.tracker.GetAlerts(c.Request.Context(), resolved)
	if err != nil {
		slog.Error("get alerts", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get alerts"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"alerts": alerts})
}

// POST /api/v1/costs/alerts/:id/resolve
func (ch *CostHandler) ResolveAlert(c *gin.Context) {
	alertID, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	actor := middleware.Actor(c)
	err := ch.tracker.ResolveAlert(c.Request.Context(), alertID, actor)
	if err != nil {
		slog.Error("resolve alert", "err", err, "alert_id", alertID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve alert"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "resolved"})
}

// POST /api/v1/orgs/:id/budget
func (ch *CostHandler) SetBudget(c *gin.Context) {
	orgID, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	// Verify org admin access
	actor := middleware.Actor(c)
	if actor != "master" {
		role, err := dbpkg.GetOrgMemberRole(c.Request.Context(), ch.h.db, orgID, actor)
		if err != nil || !models.RoleAtLeast(role, models.OrgRoleAdmin) {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin access required"})
			return
		}
	}

	var req struct {
		MonthlyLimit   float64 `json:"monthly_limit" binding:"required"`
		AlertThreshold float64 `json:"alert_threshold"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.AlertThreshold == 0 {
		req.AlertThreshold = 0.8 // Default 80%
	}

	month := time.Now().Format("2006-01")

	_, err := ch.h.db.ExecContext(c.Request.Context(), `
		INSERT INTO cost_budgets (id, org_id, monthly_limit, alert_threshold, current_month)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (org_id, current_month)
		DO UPDATE SET 
			monthly_limit = EXCLUDED.monthly_limit,
			alert_threshold = EXCLUDED.alert_threshold,
			updated_at = NOW()
	`, uuid.New(), orgID, req.MonthlyLimit, req.AlertThreshold, month)

	if err != nil {
		slog.Error("set budget", "err", err, "org_id", orgID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to set budget"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "budget updated"})
}

// GET /api/v1/costs/rates
func (ch *CostHandler) GetRates(c *gin.Context) {
	var rates []cost.CostRate
	err := ch.h.db.SelectContext(c.Request.Context(), &rates, `
		SELECT * FROM cost_rates ORDER BY active DESC, name
	`)
	if err != nil {
		slog.Error("get rates", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get rates"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"rates": rates})
}

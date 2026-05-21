package handlers

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	authpkg "github.com/nickvd7/vaultrun/internal/auth"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
)

type KeyHandler struct {
	h *Hub
}

func NewKeyHandler(h *Hub) *KeyHandler { return &KeyHandler{h: h} }

type createKeyRequest struct {
	Name      string  `json:"name" binding:"required"`
	ExpiresAt *string `json:"expires_at"` // optional RFC3339 timestamp
}

// POST /api/v1/keys
func (kh *KeyHandler) Create(c *gin.Context) {
	var req createKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid expires_at: use RFC3339 format"})
			return
		}
		if t.Before(time.Now()) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expires_at must be in the future"})
			return
		}
		expiresAt = &t
	}

	plaintext, key, err := authpkg.GenerateKey(req.Name, expiresAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "key generation failed"})
		return
	}

	if err := dbpkg.CreateAPIKey(c.Request.Context(), kh.h.db, key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist key"})
		return
	}

	actor := middleware.Actor(c)
	meta := models.JSONB{
		"key_id":   key.ID.String(),
		"key_name": key.Name,
		"prefix":   key.Prefix,
	}
	if expiresAt != nil {
		meta["expires_at"] = expiresAt.Format(time.RFC3339)
	}
	kh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:    actor,
		Action:   models.ActionAPIKeyCreated,
		Metadata: meta,
	})

	resp := gin.H{
		"id":         key.ID,
		"name":       key.Name,
		"prefix":     key.Prefix,
		"key":        plaintext, // shown exactly once — caller must save it
		"active":     key.Active,
		"created_at": key.CreatedAt,
	}
	if expiresAt != nil {
		resp["expires_at"] = expiresAt.Format(time.RFC3339)
	}
	c.JSON(http.StatusCreated, resp)
}

// GET /api/v1/keys
func (kh *KeyHandler) List(c *gin.Context) {
	keys, err := dbpkg.ListAPIKeys(c.Request.Context(), kh.h.db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list keys"})
		return
	}
	total, _ := dbpkg.CountAPIKeys(c.Request.Context(), kh.h.db)
	c.JSON(http.StatusOK, gin.H{"api_keys": keys, "total": total})
}

// DELETE /api/v1/keys/:id
func (kh *KeyHandler) Revoke(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid key id"})
		return
	}

	if err := dbpkg.RevokeAPIKey(c.Request.Context(), kh.h.db, id); err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "key not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke key"})
		return
	}

	actor := middleware.Actor(c)
	kh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:  actor,
		Action: models.ActionAPIKeyRevoked,
		Metadata: models.JSONB{
			"key_id": id.String(),
		},
	})

	c.Status(http.StatusNoContent)
}

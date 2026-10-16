package handlers

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	authpkg "github.com/nickvd7/vaultrun/internal/auth"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
)

// Block reserved actor names — these strings are used as identity sentinels
// throughout the codebase (RBAC short-circuits, audit log actor fields) and
// must never be assignable to a real API key.
var reservedActorNames = map[string]struct{}{
	"master":  {},
	"cleanup": {},
	"unknown": {},
	"system":  {},
	"":        {},
}

type KeyHandler struct {
	h *Hub
}

func NewKeyHandler(h *Hub) *KeyHandler { return &KeyHandler{h: h} }

type createKeyRequest struct {
	Name      string  `json:"name" binding:"required"`
	ExpiresAt *string `json:"expires_at"` // optional RFC3339 timestamp
	// AllowedSecrets restricts which secrets the key may inject into runs.
	// Omitted or null: any secret. Empty list: none.
	AllowedSecrets []string `json:"allowed_secrets"`
}

// POST /api/v1/keys
func (kh *KeyHandler) Create(c *gin.Context) {
	var req createKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if _, reserved := reservedActorNames[strings.ToLower(strings.TrimSpace(req.Name))]; reserved {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is reserved"})
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
		slog.Error("key generation failed", "err", err, "method", c.Request.Method, "path", c.FullPath())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "key generation failed"})
		return
	}

	if req.AllowedSecrets != nil {
		key.AllowedSecrets = normalizeSecretNames(req.AllowedSecrets)
	}

	if err := dbpkg.CreateAPIKey(c.Request.Context(), kh.h.db, key); err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "api key name already exists"})
			return
		}
		slog.Error("failed to persist key", "err", err, "method", c.Request.Method, "path", c.FullPath())
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

		"allowed_secrets": key.AllowedSecrets,
	}
	if expiresAt != nil {
		resp["expires_at"] = expiresAt.Format(time.RFC3339)
	}
	c.JSON(http.StatusCreated, resp)
}

type updateKeySecretsRequest struct {
	AllowedSecrets json.RawMessage `json:"allowed_secrets"`
}

// PUT /api/v1/keys/:id/secrets
// Body {"allowed_secrets": ["A","B"]} restricts the key; [] denies all secrets;
// null removes the restriction.
func (kh *KeyHandler) SetSecrets(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid key id"})
		return
	}
	var req updateKeySecretsRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.AllowedSecrets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "allowed_secrets is required (list of names, [] for none, null for unrestricted)"})
		return
	}
	var names []string
	if err := json.Unmarshal(req.AllowedSecrets, &names); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "allowed_secrets must be a list of strings or null"})
		return
	}
	if names != nil {
		names = normalizeSecretNames(names)
	}

	if err := dbpkg.SetAPIKeyAllowedSecrets(c.Request.Context(), kh.h.db, id, names); err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "key not found"})
		return
	} else if err != nil {
		slog.Error("failed to update key", "err", err, "method", c.Request.Method, "path", c.FullPath())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update key"})
		return
	}

	kh.h.audit.Log(c.Request.Context(), audit.Event{
		Actor:  middleware.Actor(c),
		Action: models.ActionAPIKeySecrets,
		Metadata: models.JSONB{
			"key_id":          id.String(),
			"allowed_secrets": names,
		},
	})
	c.JSON(http.StatusOK, gin.H{"id": id, "allowed_secrets": names})
}

func normalizeSecretNames(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, n := range in {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// GET /api/v1/keys
func (kh *KeyHandler) List(c *gin.Context) {
	keys, err := dbpkg.ListAPIKeys(c.Request.Context(), kh.h.db)
	if err != nil {
		slog.Error("failed to list keys", "err", err, "method", c.Request.Method, "path", c.FullPath())
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
		slog.Error("failed to revoke key", "err", err, "method", c.Request.Method, "path", c.FullPath())
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

package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	authpkg "github.com/nickvd7/vaultrun/internal/auth"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
)

type KeyHandler struct {
	h *Hub
}

func NewKeyHandler(h *Hub) *KeyHandler { return &KeyHandler{h: h} }

type createKeyRequest struct {
	Name string `json:"name" binding:"required"`
}

// POST /api/v1/keys
func (kh *KeyHandler) Create(c *gin.Context) {
	var req createKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	plaintext, key, err := authpkg.GenerateKey(req.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "key generation failed"})
		return
	}

	if err := dbpkg.CreateAPIKey(c.Request.Context(), kh.h.db, key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "persist key failed"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":         key.ID,
		"name":       key.Name,
		"prefix":     key.Prefix,
		"key":        plaintext, // shown exactly once
		"created_at": key.CreatedAt,
	})
}

// GET /api/v1/keys
func (kh *KeyHandler) List(c *gin.Context) {
	keys, err := dbpkg.ListAPIKeys(c.Request.Context(), kh.h.db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"api_keys": keys})
}

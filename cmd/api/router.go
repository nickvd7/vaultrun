package main

import (
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/cmd/api/handlers"
	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/config"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/runner"
	"github.com/nickvd7/vaultrun/internal/workspace"
)

func newRouter(
	cfg *config.Config,
	db *sqlx.DB,
	docker *dockerpkg.Client,
	ws *workspace.Manager,
	rnr *runner.Runner,
	al *audit.Logger,
) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())

	// Limit multipart memory to prevent memory exhaustion
	r.MaxMultipartMemory = cfg.Workspace.MaxFileMB * 1024 * 1024

	// CORS: only allow explicitly configured origins.
	// An empty list means no cross-origin access (same-origin only).
	corsConfig := cors.Config{
		AllowMethods:     []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-API-Key"},
		ExposeHeaders:    []string{"Content-Disposition"},
		MaxAge:           12 * time.Hour,
		AllowCredentials: false,
	}
	if len(cfg.Server.CORSOrigins) > 0 {
		corsConfig.AllowOrigins = cfg.Server.CORSOrigins
	} else {
		// No explicit origins: block all cross-origin requests.
		corsConfig.AllowOriginFunc = func(origin string) bool { return false }
	}
	r.Use(cors.New(corsConfig))

	// Security response headers
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Content-Security-Policy", "default-src 'none'")
		c.Next()
	})

	hub := handlers.NewHub(db, docker, ws, rnr, al, cfg)

	// Health — no auth, intentionally minimal (no DB internals exposed)
	health := handlers.NewHealthHandler(hub)
	r.GET("/health", health.Health)

	api := r.Group("/api/v1")

	// Key management (master key required to bootstrap)
	keys := handlers.NewKeyHandler(hub)
	api.POST("/keys", middleware.APIKeyAuth(db, cfg.Auth.MasterKey), keys.Create)
	api.GET("/keys", middleware.APIKeyAuth(db, cfg.Auth.MasterKey), keys.List)

	// All remaining routes require a valid API key.
	// Rate-limit is applied when configured (> 0).
	groupMiddleware := []gin.HandlerFunc{middleware.APIKeyAuth(db, cfg.Auth.MasterKey)}
	if cfg.Server.RateLimit > 0 {
		groupMiddleware = append([]gin.HandlerFunc{middleware.RateLimit(cfg.Server.RateLimit)}, groupMiddleware...)
	}
	authGroup := api.Group("/", groupMiddleware...)

	sessions := handlers.NewSessionHandler(hub)
	authGroup.POST("/sessions", sessions.Create)
	authGroup.GET("/sessions", sessions.List)
	authGroup.GET("/sessions/:id", sessions.Get)
	authGroup.DELETE("/sessions/:id", sessions.Delete)

	files := handlers.NewFileHandler(hub)
	authGroup.POST("/sessions/:id/files", files.Upload)
	authGroup.GET("/sessions/:id/files", files.List)
	authGroup.GET("/sessions/:id/files/*path", files.Download)

	runs := handlers.NewRunHandler(hub)
	authGroup.POST("/sessions/:id/run", runs.Execute)
	authGroup.GET("/sessions/:id/runs", runs.ListBySession)
	authGroup.GET("/runs/:id", runs.Get)

	auditH := handlers.NewAuditHandler(hub)
	authGroup.GET("/audit", auditH.List)

	// Catch-all 404 — don't leak route structure
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})

	return r
}

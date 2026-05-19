package main

import (
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

	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Authorization", "X-API-Key"}
	r.Use(cors.New(corsConfig))

	hub := handlers.NewHub(db, docker, ws, rnr, al, cfg)

	health := handlers.NewHealthHandler(hub)
	r.GET("/health", health.Health)

	api := r.Group("/api/v1")

	// Key management (master key required to bootstrap)
	keys := handlers.NewKeyHandler(hub)
	api.POST("/keys", middleware.APIKeyAuth(db, cfg.Auth.MasterKey), keys.Create)
	api.GET("/keys", middleware.APIKeyAuth(db, cfg.Auth.MasterKey), keys.List)

	// All remaining routes require a valid API key
	auth := api.Group("/", middleware.APIKeyAuth(db, cfg.Auth.MasterKey))

	sessions := handlers.NewSessionHandler(hub)
	auth.POST("/sessions", sessions.Create)
	auth.GET("/sessions", sessions.List)
	auth.GET("/sessions/:id", sessions.Get)
	auth.DELETE("/sessions/:id", sessions.Delete)

	files := handlers.NewFileHandler(hub)
	auth.POST("/sessions/:id/files", files.Upload)
	auth.GET("/sessions/:id/files", files.List)
	auth.GET("/sessions/:id/files/*path", files.Download)

	runs := handlers.NewRunHandler(hub)
	auth.POST("/sessions/:id/run", runs.Execute)
	auth.GET("/sessions/:id/runs", runs.ListBySession)
	auth.GET("/runs/:id", runs.Get)

	auditH := handlers.NewAuditHandler(hub)
	auth.GET("/audit", auditH.List)

	return r
}

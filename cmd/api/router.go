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
	"github.com/nickvd7/vaultrun/internal/policy"
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

	r.MaxMultipartMemory = cfg.Workspace.MaxFileMB * 1024 * 1024

	// CORS: only allow explicitly configured origins.
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

	hub := handlers.NewHub(db, docker, ws, rnr, al, cfg, policy.AllowAll{})

	health := handlers.NewHealthHandler(hub)
	r.GET("/health", health.Health)

	api := r.Group("/api/v1")
	authMW := middleware.APIKeyAuth(db, cfg.Auth.MasterKey)

	// Build a middleware slice that optionally prepends the rate limiter.
	buildMW := func(extra ...gin.HandlerFunc) []gin.HandlerFunc {
		var mw []gin.HandlerFunc
		if cfg.Server.RateLimit > 0 {
			mw = append(mw, middleware.RateLimit(cfg.Server.RateLimit))
		}
		mw = append(mw, authMW)
		return append(mw, extra...)
	}

	// Key management — same rate limit + master key auth
	keysH := handlers.NewKeyHandler(hub)
	api.POST("/keys", buildMW(keysH.Create)...)
	api.GET("/keys", buildMW(keysH.List)...)
	api.DELETE("/keys/:id", buildMW(keysH.Revoke)...)

	// All remaining routes — rate-limited + any valid API key
	authGroup := api.Group("/", buildMW()...)

	sessH := handlers.NewSessionHandler(hub)
	authGroup.POST("/sessions", sessH.Create)
	authGroup.GET("/sessions", sessH.List)
	authGroup.GET("/sessions/:id", sessH.Get)
	authGroup.DELETE("/sessions/:id", sessH.Delete)

	filesH := handlers.NewFileHandler(hub)
	authGroup.POST("/sessions/:id/files", filesH.Upload)
	authGroup.GET("/sessions/:id/files", filesH.List)
	authGroup.GET("/sessions/:id/files/*path", filesH.Download)
	authGroup.DELETE("/sessions/:id/files/*path", filesH.Delete)

	runsH := handlers.NewRunHandler(hub)
	authGroup.POST("/sessions/:id/run", runsH.Execute)
	authGroup.POST("/sessions/:id/run/stream", runsH.Stream)
	authGroup.GET("/sessions/:id/runs", runsH.ListBySession)
	authGroup.GET("/runs/:id", runsH.Get)

	auditH := handlers.NewAuditHandler(hub)
	authGroup.GET("/audit", auditH.List)

	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})

	return r
}

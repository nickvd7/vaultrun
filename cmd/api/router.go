package main

import (
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/nickvd7/vaultrun/cmd/api/handlers"
	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/config"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/jobqueue"
	"github.com/nickvd7/vaultrun/internal/metrics"
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
	pol policy.Hook,
	queue jobqueue.Queue,
) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())
	r.Use(metrics.HTTPMiddleware())

	// Do not trust X-Forwarded-For from arbitrary proxies; prevents rate-limit
	// bypass via IP spoofing. Operators behind a known proxy should configure
	// its CIDR here instead of nil.
	_ = r.SetTrustedProxies(nil)

	r.MaxMultipartMemory = cfg.Workspace.MaxFileMB * 1024 * 1024

	// CORS: only allow explicitly configured origins.
	// PATCH is needed for label updates.
	corsConfig := cors.Config{
		AllowMethods:     []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
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
		if cfg.TLSEnabled() {
			// Tell browsers to use HTTPS for the next year (max-age=31536000).
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	})

	hub := handlers.NewHub(db, docker, ws, rnr, al, cfg, pol, queue)

	health := handlers.NewHealthHandler(hub)
	r.GET("/health", health.Health)
	// Prometheus metrics endpoint — unauthenticated but only bound on loopback
	// in production (or protected by an external gateway).
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// API documentation — OpenAPI spec + Redoc UI.
	//
	// A relaxed Content-Security-Policy is applied to all /docs/* requests so
	// that the Redoc standalone bundle can be loaded from cdn.jsdelivr.net.
	//
	// We cannot combine r.Static("/docs", …) with r.GET("/docs/", …):
	// r.Static registers GET /docs/*filepath, and registering any explicit
	// /docs/ path on top of that wildcard causes Gin to panic at startup.
	// Instead, a single wildcard handler owns both the CSP override and the
	// trailing-slash redirect.
	docsCSP := "default-src 'none'; " +
		"script-src 'self' 'unsafe-inline' cdn.jsdelivr.net; " +
		"style-src 'self' 'unsafe-inline' fonts.googleapis.com; " +
		"font-src fonts.gstatic.com; img-src 'self' data:"
	docsFS := http.FileServer(http.Dir("docs"))
	docsHandler := func(c *gin.Context) {
		c.Header("Content-Security-Policy", docsCSP)
		fp := c.Param("filepath")
		if fp == "/" || fp == "" {
			c.Redirect(http.StatusMovedPermanently, "/docs/index.html")
			return
		}
		// Strip the /docs prefix so the file server resolves paths within
		// the docs directory (e.g. /openapi.yaml → docs/openapi.yaml).
		c.Request.URL.Path = fp
		docsFS.ServeHTTP(c.Writer, c.Request)
	}
	r.GET("/docs/*filepath", docsHandler)
	r.HEAD("/docs/*filepath", docsHandler)

	api := r.Group("/api/v1")
	authMW := middleware.APIKeyAuth(db, cfg.Auth.MasterKey)

	// Per-actor rate limit (after auth, so we know the actor identity).
	actorLimit := cfg.ActorRateLimitPerMin()

	// Build a middleware slice that optionally prepends the rate limiter.
	buildMW := func(extra ...gin.HandlerFunc) []gin.HandlerFunc {
		var mw []gin.HandlerFunc
		if cfg.Server.RateLimit > 0 {
			mw = append(mw, middleware.RateLimit(cfg.Server.RateLimit))
		}
		mw = append(mw, authMW)
		if actorLimit > 0 {
			mw = append(mw, middleware.ActorRateLimit(actorLimit))
		}
		return append(mw, extra...)
	}

	// Key management — rate-limited + master key required (L-8).
	// Only the master key may create, enumerate, or revoke API keys.
	// Any authenticated non-master key must NOT be able to mint new keys
	// or revoke keys it doesn't own.
	masterMWKeys := middleware.RequireMasterKey()
	keysH := handlers.NewKeyHandler(hub)
	api.POST("/keys", buildMW(masterMWKeys, keysH.Create)...)
	api.GET("/keys", buildMW(masterMWKeys, keysH.List)...)
	api.DELETE("/keys/:id", buildMW(masterMWKeys, keysH.Revoke)...)

	// Org management — create/list/delete requires master key.
	// GET a specific org, list members, and list org sessions are accessible to
	// org members (enforced inside the handler via requireOrgAccess).
	orgH := handlers.NewOrgHandler(hub)
	// master-only endpoints
	api.POST("/orgs", buildMW(masterMWKeys, orgH.Create)...)
	api.GET("/orgs", buildMW(masterMWKeys, orgH.List)...)
	api.DELETE("/orgs/:id", buildMW(masterMWKeys, orgH.Delete)...)
	// org-member-accessible endpoints (auth required; handler enforces RBAC)
	api.GET("/orgs/:id", buildMW(orgH.Get)...)
	api.GET("/orgs/:id/sessions", buildMW(orgH.ListSessions)...)
	api.GET("/orgs/:id/members", buildMW(orgH.ListMembers)...)
	// member management: master key or org admin (handler enforces)
	api.POST("/orgs/:id/members", buildMW(orgH.AddMember)...)
	api.DELETE("/orgs/:id/members/:principal", buildMW(orgH.RemoveMember)...)

	// All remaining routes — rate-limited + any valid API key
	authGroup := api.Group("/", buildMW()...)

	sessH := handlers.NewSessionHandler(hub)
	authGroup.POST("/sessions", sessH.Create)
	authGroup.GET("/sessions", sessH.List)
	authGroup.GET("/sessions/:id", sessH.Get)
	authGroup.DELETE("/sessions/:id", sessH.Delete)
	authGroup.PATCH("/sessions/:id/labels", sessH.UpdateLabels)

	filesH := handlers.NewFileHandler(hub)
	authGroup.POST("/sessions/:id/files", filesH.Upload)
	authGroup.GET("/sessions/:id/files", filesH.List)
	authGroup.GET("/sessions/:id/workspace.zip", filesH.WorkspaceZip)
	authGroup.GET("/sessions/:id/files/*path", filesH.Download)
	authGroup.DELETE("/sessions/:id/files/*path", filesH.Delete)

	runsH := handlers.NewRunHandler(hub)
	authGroup.POST("/sessions/:id/run", runsH.Execute)
	authGroup.POST("/sessions/:id/run/stream", runsH.Stream)
	authGroup.POST("/sessions/:id/run/async", runsH.Async)
	authGroup.GET("/sessions/:id/runs", runsH.ListBySession)
	authGroup.GET("/runs/:id", runsH.Get)

	auditH := handlers.NewAuditHandler(hub)
	authGroup.GET("/audit", auditH.List)

	// Policy endpoints expose Rego source and dry-run eval — restrict to the
	// master key so regular API keys cannot enumerate allowed commands (L-8).
	masterMW := middleware.RequireMasterKey()
	polH := handlers.NewPolicyHandler(hub)
	authGroup.GET("/policy", masterMW, polH.Get)
	authGroup.POST("/policy/eval", masterMW, polH.Eval)

	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})

	return r
}

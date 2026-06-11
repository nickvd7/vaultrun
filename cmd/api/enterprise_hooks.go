package main

import "github.com/gin-gonic/gin"

// enterpriseHooks carries optional enterprise wiring into the core router.
// The zero value — used by core-only builds, or by enterprise builds where
// no enterprise feature is configured — disables all hooks.
//
// Enterprise features (SSO: OIDC + SAML) live in the separate
// vaultrun-enterprise repository and are compiled in as an overlay with:
// go build -tags enterprise. Commercial licensing: mail@030.dev
type enterpriseHooks struct {
	// registerAuthRoutes mounts the /auth/* SSO routes (OIDC login/callback,
	// SAML metadata/login/ACS, session me/logout). Nil when SSO is not
	// configured or not compiled in.
	registerAuthRoutes func(r *gin.Engine)
}

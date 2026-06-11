//go:build !enterprise

package main

import (
	"log/slog"
	"os"

	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/config"
)

// initEnterprise is the core-build stub. SSO (OIDC + SAML) is a VaultRun
// Enterprise feature; refusing to start when it is configured but not
// compiled in is safer than silently serving without authentication routes.
func initEnterprise(cfg *config.Config, _ *sqlx.DB, _ *audit.Logger) enterpriseHooks {
	if cfg.SSO.OIDCEnabled || cfg.SSO.SAMLEnabled {
		slog.Error("SSO/SAML is a VaultRun Enterprise feature and is not compiled into this binary — " +
			"it requires the vaultrun-enterprise overlay (go build -tags enterprise) or unset the " +
			"OIDC_*/SAML_* env vars; commercial licensing: mail@030.dev")
		os.Exit(1)
	}
	return enterpriseHooks{}
}

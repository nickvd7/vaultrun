// OAuth / protected-resource auth for the HTTP MCP transport.
//
// Modes (HTTP only):
//
//  1. Static bearer — MCP_AUTH_TOKEN (required fallback / primary for local use).
//  2. OAuth Protected Resource Metadata — when MCP_OAUTH_ISSUERS is set, publish
//     /.well-known/oauth-protected-resource so clients (including Enterprise
//     Managed Authorization / EMA clients) can discover authorization servers.
//  3. Optional RFC 7662 introspection — MCP_OAUTH_INTROSPECTION_URL (+ client id/secret)
//     validates non-static bearer tokens against the IdP.
//
// EMA is primarily a client flow (extauth.EnterpriseHandler). Server-side EMA
// support is: advertise PRM, validate tokens from the enterprise auth server.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

type oauthHTTPConfig struct {
	resourceURL           string
	issuers               []string
	scopes                []string
	introspectionURL      string
	introspectionClientID string
	introspectionSecret   string
	staticToken           string
	metadataPath          string
}

func oauthConfigFromEnv(staticToken, publicBaseURL string) oauthHTTPConfig {
	cfg := oauthHTTPConfig{
		staticToken:  staticToken,
		resourceURL:  strings.TrimRight(publicBaseURL, "/") + "/mcp",
		scopes:       []string{"mcp"},
		metadataPath: "/.well-known/oauth-protected-resource",
	}
	if v := os.Getenv("MCP_RESOURCE_URL"); v != "" {
		cfg.resourceURL = v
	}
	if v := os.Getenv("MCP_OAUTH_ISSUERS"); v != "" {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfg.issuers = append(cfg.issuers, p)
			}
		}
	}
	if v := os.Getenv("MCP_OAUTH_SCOPES"); v != "" {
		cfg.scopes = nil
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfg.scopes = append(cfg.scopes, p)
			}
		}
	}
	cfg.introspectionURL = os.Getenv("MCP_OAUTH_INTROSPECTION_URL")
	cfg.introspectionClientID = os.Getenv("MCP_OAUTH_INTROSPECTION_CLIENT_ID")
	cfg.introspectionSecret = os.Getenv("MCP_OAUTH_INTROSPECTION_CLIENT_SECRET")
	return cfg
}

func (c oauthHTTPConfig) prmEnabled() bool {
	return len(c.issuers) > 0
}

func (c oauthHTTPConfig) metadata() *oauthex.ProtectedResourceMetadata {
	return &oauthex.ProtectedResourceMetadata{
		Resource:               c.resourceURL,
		AuthorizationServers:   append([]string(nil), c.issuers...),
		ScopesSupported:        append([]string(nil), c.scopes...),
		BearerMethodsSupported: []string{"header"},
	}
}

func (c oauthHTTPConfig) metadataURL() string {
	base := strings.TrimSuffix(c.resourceURL, "/mcp")
	return strings.TrimRight(base, "/") + c.metadataPath
}

// newBearerAuthMiddleware returns SDK auth middleware that accepts the static
// MCP_AUTH_TOKEN and, when configured, tokens validated via introspection.
func newBearerAuthMiddleware(cfg oauthHTTPConfig) func(http.Handler) http.Handler {
	opts := &auth.RequireBearerTokenOptions{
		AllowMissingExpiration: true,
		ClockSkew:              30 * time.Second,
	}
	if cfg.prmEnabled() {
		opts.ResourceMetadataURL = cfg.metadataURL()
	}

	verifier := func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		if cfg.staticToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(cfg.staticToken)) == 1 {
			return &auth.TokenInfo{
				Scopes: cfg.scopes,
				UserID: "static-token",
				Extra:  map[string]any{"auth": "static"},
			}, nil
		}
		if cfg.introspectionURL == "" {
			return nil, auth.ErrInvalidToken
		}
		return introspectToken(ctx, token, cfg)
	}

	return auth.RequireBearerToken(verifier, opts)
}

func introspectToken(ctx context.Context, token string, cfg oauthHTTPConfig) (*auth.TokenInfo, error) {
	data := url.Values{}
	data.Set("token", token)
	data.Set("token_type_hint", "access_token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.introspectionURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if cfg.introspectionClientID != "" {
		req.SetBasicAuth(cfg.introspectionClientID, cfg.introspectionSecret)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("vaultrun-mcp: introspection request failed", "err", err)
		return nil, auth.ErrInvalidToken
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: introspection status %d", auth.ErrInvalidToken, resp.StatusCode)
	}

	var result struct {
		Active bool   `json:"active"`
		Scope  string `json:"scope"`
		Exp    int64  `json:"exp"`
		Sub    string `json:"sub"`
		Aud    any    `json:"aud"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, auth.ErrInvalidToken
	}
	if !result.Active {
		return nil, auth.ErrInvalidToken
	}

	info := &auth.TokenInfo{
		Scopes: strings.Fields(result.Scope),
		UserID: result.Sub,
		Extra:  map[string]any{"auth": "introspection"},
	}
	if result.Exp > 0 {
		info.Expiration = time.Unix(result.Exp, 0)
	}
	return info, nil
}

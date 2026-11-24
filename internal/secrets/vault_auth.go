package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

func staticVaultToken(token string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return token, nil }
}

// appRoleSource logs in with AppRole and caches the client token until
// shortly before its lease ends. The secret id and the token are never logged.
type appRoleSource struct {
	addr      string
	namespace string
	roleID    string
	secretID  string

	mu     sync.Mutex
	cached string
	expiry time.Time
}

func newAppRoleToken(addr, namespace, roleID, secretID string) *appRoleSource {
	return &appRoleSource{
		addr:      addr,
		namespace: namespace,
		roleID:    roleID,
		secretID:  secretID,
	}
}

func (s *appRoleSource) token(ctx context.Context, client *http.Client) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != "" && time.Now().Before(s.expiry) {
		return s.cached, nil
	}
	body, err := json.Marshal(map[string]string{
		"role_id":   s.roleID,
		"secret_id": s.secretID,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.addr+"/v1/auth/approle/login", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("vault: build approle login: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.namespace != "" {
		req.Header.Set("X-Vault-Namespace", s.namespace)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vault: approle login: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		slog.Debug("vault: approle login failed", "status", resp.StatusCode, "body_len", len(raw))
		return "", fmt.Errorf("vault: approle login failed (status %d)", resp.StatusCode)
	}
	var out struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int    `json:"lease_duration"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("vault: decode approle login: %w", err)
	}
	if out.Auth.ClientToken == "" {
		return "", fmt.Errorf("vault: approle login returned no token")
	}
	ttl := time.Duration(out.Auth.LeaseDuration) * time.Second
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	refresh := ttl - 30*time.Second
	if refresh < time.Second {
		refresh = time.Second
	}
	s.cached = out.Auth.ClientToken
	s.expiry = time.Now().Add(refresh)
	return s.cached, nil
}

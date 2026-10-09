package localgateway

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	vaultrun "github.com/nickvd7/vaultrun/sdk/go"
)

// SessionStore maps conversation keys to VaultRun session IDs.
type SessionStore struct {
	mu   sync.Mutex
	byID map[string]string // conversationKey → sessionID
}

// NewSessionStore creates an empty in-memory conversation→session map.
func NewSessionStore() *SessionStore {
	return &SessionStore{byID: make(map[string]string)}
}

// Get returns a previously bound session ID, if any.
func (s *SessionStore) Get(conversationKey string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byID[conversationKey]
	return id, ok
}

// Put binds a conversation key to a session ID.
func (s *SessionStore) Put(conversationKey, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[conversationKey] = sessionID
}

// Delete removes a binding.
func (s *SessionStore) Delete(conversationKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, conversationKey)
}

// parseSessionID validates a client-supplied session UUID.
func parseSessionID(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("session id is empty")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("session id must be a UUID")
	}
	return id.String(), nil
}

// ensureSession returns a running session ID: explicit header, store hit, or create.
func (g *Gateway) ensureSession(ctx context.Context, conversationKey, explicitSessionID string) (string, error) {
	if explicitSessionID != "" {
		id, err := parseSessionID(explicitSessionID)
		if err != nil {
			return "", err
		}
		sess, err := g.vr.GetSession(ctx, id)
		if err != nil {
			return "", fmt.Errorf("lookup session: %w", err)
		}
		if sess.Status != "running" {
			return "", fmt.Errorf("session %s is not running (status=%s)", id, sess.Status)
		}
		if conversationKey != "" {
			g.sessions.Put(conversationKey, id)
		}
		return id, nil
	}

	if conversationKey != "" {
		if id, ok := g.sessions.Get(conversationKey); ok {
			sess, err := g.vr.GetSession(ctx, id)
			if err == nil && sess.Status == "running" {
				return id, nil
			}
			g.sessions.Delete(conversationKey)
		}
	}

	name := "local-gateway"
	if conversationKey != "" {
		// Keep name short / safe for API.
		suffix := conversationKey
		if len(suffix) > 24 {
			suffix = suffix[:24]
		}
		name = "local-gateway-" + suffix
	}
	sess, err := g.vr.CreateSession(ctx, vaultrun.CreateSessionOptions{
		Name:           name,
		Image:          g.cfg.DefaultImage,
		NetworkEnabled: g.cfg.NetworkEnabled,
		TimeoutSeconds: g.cfg.MaxRunTimeoutSeconds,
		Labels: map[string]string{
			"vaultrun.local_gateway": "true",
			"vaultrun.conversation":  truncateLabel(conversationKey),
		},
	})
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	if conversationKey != "" {
		g.sessions.Put(conversationKey, sess.ID)
	}
	return sess.ID, nil
}

func truncateLabel(s string) string {
	if s == "" {
		return "none"
	}
	// Label values should stay compact.
	if len(s) > 63 {
		return s[:63]
	}
	return s
}

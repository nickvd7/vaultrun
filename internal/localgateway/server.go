package localgateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	headerSessionID      = "X-VaultRun-Session-Id"
	headerConversationID = "X-VaultRun-Conversation-Id"
	maxConversationLen   = 128
)

// Handler returns the HTTP handler for the gateway (for ListenAndServe or tests).
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", g.handleHealthz)
	mux.HandleFunc("/v1/models", g.handleModels)
	mux.HandleFunc("/v1/chat/completions", g.handleChatCompletions)
	return g.wrap(mux)
}

// Server builds an *http.Server with hardened timeouts.
func (g *Gateway) Server() *http.Server {
	return &http.Server{
		Addr:              g.cfg.ListenAddr,
		Handler:           g.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      g.cfg.UpstreamTimeout + time.Duration(g.cfg.MaxToolLoops)*time.Duration(g.cfg.MaxRunTimeoutSeconds)*time.Second + 30*time.Second,
		IdleTimeout:       90 * time.Second,
	}
}

func (g *Gateway) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")

		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		ip := peerIP(r)
		if !g.limiter.allow(ip) {
			writeErr(w, http.StatusTooManyRequests, "rate_limit", "rate limit exceeded")
			return
		}

		if !g.authorize(r) {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid or missing bearer token")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (g *Gateway) authorize(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	token := strings.TrimSpace(auth[len(prefix):])
	if token == "" {
		return false
	}
	expected := g.cfg.AuthToken
	if len(token) != len(expected) {
		// Constant-time compare still, against expected, to avoid length oracle
		// on the happy path; mismatch length is already fail.
		subtle.ConstantTimeCompare([]byte(expected), []byte(expected))
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func (g *Gateway) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok","service":"vaultrun-local-gateway"}`))
}

func (g *Gateway) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	models, err := g.upstream.ListModels(ctx)
	if err != nil {
		// Fallback: advertise default model so clients can still start.
		if g.cfg.DefaultModel != "" {
			writeJSON(w, http.StatusOK, ModelsResponse{
				Object: "list",
				Data: []ModelObject{{
					ID:      g.cfg.DefaultModel,
					Object:  "model",
					OwnedBy: "local",
				}},
			})
			return
		}
		writeErr(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func (g *Gateway) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, g.cfg.MaxBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeErr(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body too large")
			return
		}
		writeErr(w, http.StatusBadRequest, "invalid_request", "unable to read body")
		return
	}

	var req ChatRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}

	conversationKey := strings.TrimSpace(r.Header.Get(headerConversationID))
	if len(conversationKey) > maxConversationLen {
		writeErr(w, http.StatusBadRequest, "invalid_request", "conversation id too long")
		return
	}
	if conversationKey != "" && !safeConversationID(conversationKey) {
		writeErr(w, http.StatusBadRequest, "invalid_request", "conversation id has invalid characters")
		return
	}
	explicitSession := strings.TrimSpace(r.Header.Get(headerSessionID))

	// Bound the whole tool loop.
	timeout := g.cfg.UpstreamTimeout + time.Duration(g.cfg.MaxToolLoops*g.cfg.MaxRunTimeoutSeconds)*time.Second
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	result, err := g.RunChatLoop(ctx, req, conversationKey, explicitSession)
	if err != nil {
		var ge *GatewayError
		if errors.As(err, &ge) {
			writeErr(w, ge.Status, ge.Code, ge.Message)
			return
		}
		slog.Error("localgateway: unexpected error", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	w.Header().Set(headerSessionID, result.SessionID)
	writeJSON(w, http.StatusOK, result.Response)
}

func safeConversationID(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == ':' {
			continue
		}
		return false
	}
	return true
}

func peerIP(r *http.Request) string {
	// Never trust X-Forwarded-For by default (same posture as MCP HTTP).
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorBody{Error: ErrorDetail{
		Message: message,
		Type:    "invalid_request_error",
		Code:    code,
	}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

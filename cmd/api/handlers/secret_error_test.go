package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/nickvd7/vaultrun/internal/secrets"
)

func TestSecretClientErrorSplitsNotFoundFromBackend(t *testing.T) {
	code, msg := secretClientError("DB_PASSWORD", &secrets.NotFoundError{Name: "DB_PASSWORD"})
	if code != http.StatusBadRequest || msg != "secret not found: DB_PASSWORD" {
		t.Fatalf("not found: %d %q", code, msg)
	}

	code, msg = secretClientError("DB_PASSWORD", errors.New("vault: approle login failed (status 403)"))
	if code != http.StatusBadGateway || msg != "secret backend unavailable: DB_PASSWORD" {
		t.Fatalf("backend: %d %q", code, msg)
	}
	if strings.Contains(msg, "approle") || strings.Contains(msg, "403") {
		t.Fatalf("response leaked backend detail: %q", msg)
	}

	_, err := (&secrets.EnvProvider{}).GetSecret(context.Background(), "../x")
	code, msg = secretClientError("../x", err)
	if code != http.StatusBadRequest || msg != "invalid secret name" {
		t.Fatalf("invalid name: %d %q", code, msg)
	}
}

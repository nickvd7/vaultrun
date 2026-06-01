// Package secrets provides a pluggable secrets broker for VaultRun.
//
// At run time, callers request named secrets (e.g. "DB_PASSWORD", "API_KEY").
// The broker resolves them from the configured backend and injects them as
// environment variables into the container exec — they are never stored in the
// database or logged.
//
// Supported backends (configured via SECRETS_PROVIDER env var):
//
//   - "env"   — read VAULTRUN_SECRET_<NAME> from the server process environment (default)
//   - "vault" — HashiCorp Vault KV v2 via HTTP API
//   - "aws"   — AWS Secrets Manager via HTTP API (SigV4 signed)
package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"time"

	"github.com/nickvd7/vaultrun/internal/httputil"
)

// Provider is the secrets broker interface.
type Provider interface {
	// GetSecret returns the plaintext value for the named secret.
	// Returns an error if the secret does not exist or cannot be fetched.
	GetSecret(ctx context.Context, name string) (string, error)

	// Name returns a human-readable backend name for diagnostics (never logs secrets).
	Name() string
}

// validateSecretName rejects names that could cause path traversal in Vault/AWS
// URL construction or OS env var manipulation. Only alphanumeric, underscore,
// and hyphen characters are allowed; max 128 characters.
func validateSecretName(name string) error {
	if name == "" {
		return fmt.Errorf("secret name must not be empty")
	}
	if len(name) > 128 {
		return fmt.Errorf("secret name exceeds maximum length of 128 characters")
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-') {
			return fmt.Errorf("secret name %q contains disallowed character %q (only [a-zA-Z0-9_-] permitted)", name, r)
		}
	}
	return nil
}

// EnvProvider reads secrets from the server's process environment.
// Each secret named "FOO" is looked up as VAULTRUN_SECRET_FOO.
// Ideal for development and single-host deployments.
type EnvProvider struct{}

func (p *EnvProvider) Name() string { return "env" }

func (p *EnvProvider) GetSecret(_ context.Context, name string) (string, error) {
	if err := validateSecretName(name); err != nil {
		return "", err
	}
	key := "VAULTRUN_SECRET_" + strings.ToUpper(name)
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("secret %q not found (env var %s is unset)", name, key)
	}
	return v, nil
}

// VaultProvider fetches secrets from a HashiCorp Vault KV v2 mount.
//
// Configuration env vars:
//
//	VAULT_ADDR   — e.g. "https://vault.example.com"
//	VAULT_TOKEN  — Vault token (service account / app role)
//	VAULT_MOUNT  — KV v2 mount path (default: "secret")
//	VAULT_PATH   — base path inside the mount (default: "vaultrun")
//
// The secret "DB_PASSWORD" is fetched from:
//
//	GET {VAULT_ADDR}/v1/{VAULT_MOUNT}/data/{VAULT_PATH}/DB_PASSWORD
//
// and the value at data.data.value is returned.
type VaultProvider struct {
	addr   string
	token  string
	mount  string
	path   string
	client *http.Client
}

func NewVaultProvider(addr, token, mount, path string) *VaultProvider {
	if mount == "" {
		mount = "secret"
	}
	if path == "" {
		path = "vaultrun"
	}
	return &VaultProvider{
		addr:   strings.TrimRight(addr, "/"),
		token:  token,
		mount:  mount,
		path:   path,
		client: httputil.NoRedirectClient(10 * time.Second),
	}
}

func (p *VaultProvider) Name() string { return "vault" }

func (p *VaultProvider) GetSecret(ctx context.Context, name string) (string, error) {
	if err := validateSecretName(name); err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/v1/%s/data/%s/%s", p.addr, p.mount, p.path, neturl.PathEscape(name))
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("vault: build request: %w", err)
	}
	req.Header.Set("X-Vault-Token", p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vault: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("vault: secret %q not found", name)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vault: unexpected status %d for secret %q: %s", resp.StatusCode, name, body)
	}

	// KV v2 response: {"data":{"data":{"value":"<secret>"},...},...}
	var result struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("vault: decode response: %w", err)
	}
	v, ok := result.Data.Data["value"]
	if !ok {
		// Fall back to the key name itself as the field name inside the KV secret.
		v, ok = result.Data.Data[name]
		if !ok {
			return "", fmt.Errorf("vault: secret %q exists but has no 'value' or %q field", name, name)
		}
	}
	return v, nil
}

// AWSProvider fetches secrets from AWS Secrets Manager using the AWS REST API.
//
// Configuration env vars (standard AWS SDK env vars are honoured):
//
//	AWS_REGION             — AWS region (required if not in instance metadata)
//	AWS_ACCESS_KEY_ID      — AWS access key (or use instance IAM role)
//	AWS_SECRET_ACCESS_KEY  — AWS secret key (or use instance IAM role)
//	AWS_SESSION_TOKEN      — optional session token
//	SECRETS_AWS_PREFIX     — optional prefix prepended to secret names (default: "vaultrun/")
//
// The secret "DB_PASSWORD" is fetched by secret name "{prefix}DB_PASSWORD".
// The returned SecretString is used directly.
type AWSProvider struct {
	region string
	prefix string
	client *http.Client
}

func NewAWSProvider(region, prefix string) *AWSProvider {
	if region == "" {
		region = os.Getenv("AWS_REGION")
		if region == "" {
			region = os.Getenv("AWS_DEFAULT_REGION")
		}
	}
	if prefix == "" {
		prefix = os.Getenv("SECRETS_AWS_PREFIX")
		if prefix == "" {
			prefix = "vaultrun/"
		}
	}
	return &AWSProvider{
		region: region,
		prefix: prefix,
		client: httputil.NoRedirectClient(10 * time.Second),
	}
}

func (p *AWSProvider) Name() string { return "aws" }

func (p *AWSProvider) GetSecret(ctx context.Context, name string) (string, error) {
	if err := validateSecretName(name); err != nil {
		return "", err
	}
	// Use the AWS Secrets Manager endpoint directly.
	// We implement a minimal SigV4 signing flow so we don't need the full AWS SDK.
	secretID := p.prefix + name
	endpoint := fmt.Sprintf("https://secretsmanager.%s.amazonaws.com", p.region)

	body := fmt.Sprintf(`{"SecretId":%q}`, secretID)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("aws: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "secretsmanager.GetSecretValue")

	// Sign using AWS SigV4
	if err := signAWS(req, p.region, "secretsmanager", []byte(body)); err != nil {
		return "", fmt.Errorf("aws: sign request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("aws: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		// Log full details at debug level only — don't expose the secret ID or
		// AWS error body in the returned error (it may reach the API response).
		slog.Debug("aws: secrets manager error", "secret_id", secretID, "status", resp.StatusCode, "body", string(respBody))
		return "", fmt.Errorf("aws: get secret failed (status %d)", resp.StatusCode)
	}

	var result struct {
		SecretString string `json:"SecretString"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("aws: decode response: %w", err)
	}
	if result.SecretString == "" {
		return "", fmt.Errorf("aws: secret %q has no SecretString (binary secrets not supported)", secretID)
	}
	return result.SecretString, nil
}

// New returns the configured Provider based on the SECRETS_PROVIDER env var.
//   - "vault" → VaultProvider (requires VAULT_ADDR + VAULT_TOKEN)
//   - "aws"   → AWSProvider (requires AWS_REGION + credentials)
//   - ""/"env"→ EnvProvider (default; no external dependencies)
func New() Provider {
	switch strings.ToLower(os.Getenv("SECRETS_PROVIDER")) {
	case "vault":
		return NewVaultProvider(
			os.Getenv("VAULT_ADDR"),
			os.Getenv("VAULT_TOKEN"),
			os.Getenv("VAULT_MOUNT"),
			os.Getenv("VAULT_PATH"),
		)
	case "aws":
		return NewAWSProvider(
			os.Getenv("AWS_REGION"),
			os.Getenv("SECRETS_AWS_PREFIX"),
		)
	default:
		return &EnvProvider{}
	}
}

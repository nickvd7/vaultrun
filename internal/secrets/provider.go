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
//   - "azure" — Azure Key Vault via HTTP API
package secrets

import (
	"bytes"
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
		return &nameError{fmt.Errorf("secret name must not be empty")}
	}
	if len(name) > 128 {
		return &nameError{fmt.Errorf("secret name exceeds maximum length of 128 characters")}
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-') {
			return &nameError{fmt.Errorf("secret name %q contains disallowed character %q (only [a-zA-Z0-9_-] permitted)", name, r)}
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
		return "", &NotFoundError{Name: name}
	}
	return v, nil
}

// VaultProvider fetches secrets from a HashiCorp Vault KV v2 mount.
//
// Configuration env vars:
//
//	VAULT_ADDR      — e.g. "https://vault.example.com"
//	VAULT_TOKEN     — Vault token. When empty, VAULT_ROLE_ID and
//	                  VAULT_SECRET_ID log in with AppRole.
//	VAULT_MOUNT     — KV v2 mount path (default: "secret")
//	VAULT_PATH      — base path inside the mount (default: "vaultrun")
//	VAULT_NAMESPACE — optional X-Vault-Namespace header
//	VAULT_FIELD     — field inside the secret (default: "value", then the secret name)
//	VAULT_VERSION   — optional KV v2 version (digits only)
//
// The secret "DB_PASSWORD" is fetched from:
//
//	GET {VAULT_ADDR}/v1/{VAULT_MOUNT}/data/{VAULT_PATH}/DB_PASSWORD
//
// and the value at data.data.value is returned.
type VaultProvider struct {
	addr      string
	mount     string
	path      string
	namespace string
	field     string
	version   string
	token     func(context.Context) (string, error)
	client    *http.Client
}

// VaultConfig is how the API process reaches a KV v2 mount.
type VaultConfig struct {
	Addr, Token, Mount, Path, Namespace, Field, Version string
	RoleID, SecretID                                    string
}

func NewVaultProvider(cfg VaultConfig) (*VaultProvider, error) {
	if cfg.Mount == "" {
		cfg.Mount = "secret"
	}
	if cfg.Path == "" {
		cfg.Path = "vaultrun"
	}
	if cfg.Version != "" {
		for _, r := range cfg.Version {
			if r < '0' || r > '9' {
				return nil, fmt.Errorf("vault: VAULT_VERSION must be digits")
			}
		}
	}
	p := &VaultProvider{
		addr:      strings.TrimRight(cfg.Addr, "/"),
		mount:     cfg.Mount,
		path:      cfg.Path,
		namespace: cfg.Namespace,
		field:     cfg.Field,
		version:   cfg.Version,
		client:    httputil.NoRedirectClient(10 * time.Second),
	}
	switch {
	case cfg.Token != "":
		p.token = staticVaultToken(cfg.Token)
	case cfg.RoleID != "" && cfg.SecretID != "":
		src := newAppRoleToken(p.addr, cfg.Namespace, cfg.RoleID, cfg.SecretID)
		p.token = func(ctx context.Context) (string, error) {
			return src.token(ctx, p.client)
		}
	default:
		p.token = func(context.Context) (string, error) {
			return "", fmt.Errorf("vault: set VAULT_TOKEN, or VAULT_ROLE_ID and VAULT_SECRET_ID")
		}
	}
	return p, nil
}

func (p *VaultProvider) Name() string { return "vault" }

func (p *VaultProvider) GetSecret(ctx context.Context, name string) (string, error) {
	if err := validateSecretName(name); err != nil {
		return "", err
	}
	token, err := p.token(ctx)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/v1/%s/data/%s/%s", p.addr, p.mount, p.path, neturl.PathEscape(name))
	if p.version != "" {
		url += "?version=" + p.version
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("vault: build request: %w", err)
	}
	req.Header.Set("X-Vault-Token", token)
	if p.namespace != "" {
		req.Header.Set("X-Vault-Namespace", p.namespace)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vault: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return "", &NotFoundError{Name: name}
	}
	if resp.StatusCode != http.StatusOK {
		// Log full details server-side only — the raw Vault error body must not
		// propagate to API callers (it may contain internal path or policy info).
		slog.Debug("vault: unexpected response", "secret", name, "status", resp.StatusCode, "body_len", len(body))
		return "", fmt.Errorf("vault: unexpected status %d for secret %q", resp.StatusCode, name)
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
	field := p.field
	if field == "" {
		field = "value"
	}
	v, ok := result.Data.Data[field]
	if !ok && p.field == "" {
		// Fall back to the key name itself as the field name inside the KV secret.
		v, ok = result.Data.Data[name]
	}
	if !ok {
		return "", fmt.Errorf("vault: secret %q exists but has no %q field", name, field)
	}
	return v, nil
}

// AWSProvider fetches secrets from AWS Secrets Manager using the AWS REST API.
//
// Configuration env vars (standard AWS SDK env vars are honoured):
//
//	AWS_REGION             — AWS region (instance metadata is used when unset)
//	AWS_ACCESS_KEY_ID      — AWS access key (or the instance IAM role via IMDSv2)
//	AWS_SECRET_ACCESS_KEY  — AWS secret key (or the instance IAM role via IMDSv2)
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

// regionName uses the configured region, then the standard env vars, then
// the instance identity document from IMDSv2.
func (p *AWSProvider) regionName(ctx context.Context) (string, error) {
	if p.region != "" {
		return p.region, nil
	}
	if r := os.Getenv("AWS_REGION"); r != "" {
		return r, nil
	}
	if r := os.Getenv("AWS_DEFAULT_REGION"); r != "" {
		return r, nil
	}
	return imdsRegion(ctx)
}

func (p *AWSProvider) GetSecret(ctx context.Context, name string) (string, error) {
	if err := validateSecretName(name); err != nil {
		return "", err
	}
	// Use the AWS Secrets Manager endpoint directly.
	// We implement a minimal SigV4 signing flow so we don't need the full AWS SDK.
	region, err := p.regionName(ctx)
	if err != nil {
		return "", err
	}
	secretID := p.prefix + name
	endpoint := fmt.Sprintf("https://secretsmanager.%s.amazonaws.com", region)

	body := fmt.Sprintf(`{"SecretId":%q}`, secretID)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("aws: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "secretsmanager.GetSecretValue")

	// Sign using AWS SigV4
	if err := signAWS(req, region, "secretsmanager", []byte(body)); err != nil {
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
		slog.Debug("aws: secrets manager error", "secret_id", secretID, "status", resp.StatusCode, "body_len", len(respBody))
		return "", awsSecretError(name, resp.StatusCode, respBody)
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

func awsSecretError(name string, status int, body []byte) error {
	if bytes.Contains(body, []byte("ResourceNotFoundException")) {
		return &NotFoundError{Name: name}
	}
	return fmt.Errorf("aws: get secret failed (status %d)", status)
}

// New returns the configured Provider based on the SECRETS_PROVIDER env var.
//   - "vault" → VaultProvider (VAULT_ADDR plus a token or AppRole)
//   - "aws"   → AWSProvider (access keys, or the instance role via IMDSv2)
//   - "azure" → AzureProvider (Key Vault; service principal or managed identity)
//   - ""/"env"→ EnvProvider (default; no external dependencies)
func New() Provider {
	switch strings.ToLower(os.Getenv("SECRETS_PROVIDER")) {
	case "vault":
		p, err := NewVaultProvider(VaultConfig{
			Addr:      os.Getenv("VAULT_ADDR"),
			Token:     os.Getenv("VAULT_TOKEN"),
			Mount:     os.Getenv("VAULT_MOUNT"),
			Path:      os.Getenv("VAULT_PATH"),
			Namespace: os.Getenv("VAULT_NAMESPACE"),
			Field:     os.Getenv("VAULT_FIELD"),
			Version:   os.Getenv("VAULT_VERSION"),
			RoleID:    os.Getenv("VAULT_ROLE_ID"),
			SecretID:  os.Getenv("VAULT_SECRET_ID"),
		})
		if err != nil {
			return errorProvider{name: "vault", err: err}
		}
		return p
	case "aws":
		return NewAWSProvider(
			os.Getenv("AWS_REGION"),
			os.Getenv("SECRETS_AWS_PREFIX"),
		)
	case "azure":
		return NewAzureProvider(AzureConfig{
			VaultURL:     os.Getenv("AZURE_KEYVAULT_URL"),
			TenantID:     os.Getenv("AZURE_TENANT_ID"),
			ClientID:     os.Getenv("AZURE_CLIENT_ID"),
			ClientSecret: os.Getenv("AZURE_CLIENT_SECRET"),
		})
	default:
		return &EnvProvider{}
	}
}

// errorProvider keeps the API up when the broker is misconfigured. The
// failure is reported when a run asks for a secret, not at process start.
type errorProvider struct {
	name string
	err  error
}

func (p errorProvider) Name() string { return p.name }

func (p errorProvider) GetSecret(context.Context, string) (string, error) { return "", p.err }

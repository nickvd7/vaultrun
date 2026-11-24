package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nickvd7/vaultrun/internal/httputil"
)

// azureLoginBase is the Entra token host. Tests point it at httptest.
// It is not an environment variable.
var azureLoginBase = "https://login.microsoftonline.com"

// AzureConfig reaches one Key Vault. A client secret uses a service
// principal. Without one, the API process uses the instance managed identity.
type AzureConfig struct {
	VaultURL     string
	TenantID     string
	ClientID     string
	ClientSecret string
}

type AzureProvider struct {
	vaultURL     string
	tenantID     string
	clientID     string
	clientSecret string
	client       *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

func NewAzureProvider(cfg AzureConfig) *AzureProvider {
	return &AzureProvider{
		vaultURL:     strings.TrimRight(cfg.VaultURL, "/"),
		tenantID:     cfg.TenantID,
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		client:       httputil.NoRedirectClient(10 * time.Second),
	}
}

func (p *AzureProvider) Name() string { return "azure" }

func (p *AzureProvider) GetSecret(ctx context.Context, name string) (string, error) {
	if err := validateSecretName(name); err != nil {
		return "", err
	}
	if p.vaultURL == "" {
		return "", fmt.Errorf("azure: AZURE_KEYVAULT_URL is required")
	}
	tok, err := p.accessToken(ctx)
	if err != nil {
		return "", err
	}
	u := p.vaultURL + "/secrets/" + url.PathEscape(name) + "?api-version=7.4"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("azure: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("azure: request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return "", &NotFoundError{Name: name}
	}
	if resp.StatusCode != http.StatusOK {
		slog.Debug("azure: key vault error", "secret", name, "status", resp.StatusCode, "body_len", len(body))
		return "", fmt.Errorf("azure: get secret failed (status %d)", resp.StatusCode)
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("azure: decode response: %w", err)
	}
	if out.Value == "" {
		return "", fmt.Errorf("azure: secret %q has an empty value", name)
	}
	return out.Value, nil
}

func (p *AzureProvider) accessToken(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.token != "" && time.Now().Before(p.expiry) {
		return p.token, nil
	}
	var tok string
	var ttl time.Duration
	var err error
	if p.clientSecret != "" {
		tok, ttl, err = p.clientCredentials(ctx)
	} else {
		tok, ttl, err = p.managedIdentity(ctx)
	}
	if err != nil {
		return "", err
	}
	refresh := ttl - time.Minute
	if refresh < time.Second {
		refresh = time.Second
	}
	p.token = tok
	p.expiry = time.Now().Add(refresh)
	return tok, nil
}

func (p *AzureProvider) clientCredentials(ctx context.Context) (string, time.Duration, error) {
	if p.tenantID == "" || p.clientID == "" {
		return "", 0, fmt.Errorf("azure: AZURE_TENANT_ID, AZURE_CLIENT_ID and AZURE_CLIENT_SECRET are required")
	}
	form := url.Values{
		"client_id":     {p.clientID},
		"client_secret": {p.clientSecret},
		"scope":         {"https://vault.azure.net/.default"},
		"grant_type":    {"client_credentials"},
	}
	endpoint := strings.TrimRight(azureLoginBase, "/") + "/" + url.PathEscape(p.tenantID) + "/oauth2/v2.0/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return p.readToken(req, "service principal")
}

func (p *AzureProvider) managedIdentity(ctx context.Context) (string, time.Duration, error) {
	q := url.Values{
		"api-version": {"2018-02-01"},
		"resource":    {"https://vault.azure.net"},
	}
	if p.clientID != "" {
		q.Set("client_id", p.clientID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imdsBase+"/metadata/identity/oauth2/token?"+q.Encode(), nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Metadata", "true")
	resp, err := imdsClient().Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("azure: managed identity: %w", err)
	}
	return decodeTokenResponse(resp, "managed identity")
}

func (p *AzureProvider) readToken(req *http.Request, kind string) (string, time.Duration, error) {
	resp, err := p.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("azure: %s token: %w", kind, err)
	}
	return decodeTokenResponse(resp, kind)
}

func decodeTokenResponse(resp *http.Response, kind string) (string, time.Duration, error) {
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		slog.Debug("azure: token request failed", "kind", kind, "status", resp.StatusCode, "body_len", len(raw))
		return "", 0, fmt.Errorf("azure: %s token failed (status %d)", kind, resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", 0, fmt.Errorf("azure: decode %s token: %w", kind, err)
	}
	if out.AccessToken == "" {
		return "", 0, fmt.Errorf("azure: %s token response has no access token", kind)
	}
	ttl := time.Duration(out.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	return out.AccessToken, ttl, nil
}

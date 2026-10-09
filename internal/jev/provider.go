package jev

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nickvd7/vaultrun/internal/httputil"
)

// Provider identifies which System One backend to use.
type Provider string

const (
	ProviderTypeSafe Provider = "typesafe"
	ProviderOpenJEV  Provider = "openjev"
)

const (
	TypeSafeBaseURL = "https://api.typesafe.ai"
	OpenJEVBaseURL  = "https://api.openjev.sh"
	TypeSafeModel   = "jev-latest"
	OpenJEVModel    = "openjev"
)

// ResolveProvider picks typesafe | openjev from env.
// Explicit JEV_PROVIDER wins; otherwise OPENJEV_API_KEY → openjev, else typesafe.
func ResolveProvider(getenv func(string) string) Provider {
	switch strings.ToLower(strings.TrimSpace(getenv("JEV_PROVIDER"))) {
	case "openjev", "openjev.sh", "oj":
		return ProviderOpenJEV
	case "typesafe", "jev", "ts":
		return ProviderTypeSafe
	}
	if strings.TrimSpace(getenv("OPENJEV_API_KEY")) != "" &&
		strings.TrimSpace(getenv("TYPESAFE_API_KEY")) == "" &&
		strings.TrimSpace(getenv("JEV_API_KEY")) == "" {
		return ProviderOpenJEV
	}
	return ProviderTypeSafe
}

type providerDefaults struct {
	BaseURL string
	Model   string
	KeyEnvs []string
	BaseEnv string
}

func defaultsFor(p Provider) providerDefaults {
	switch p {
	case ProviderOpenJEV:
		return providerDefaults{
			BaseURL: OpenJEVBaseURL,
			Model:   OpenJEVModel,
			KeyEnvs: []string{"OPENJEV_API_KEY", "JEV_API_KEY", "TYPESAFE_API_KEY"},
			BaseEnv: "OPENJEV_BASE_URL",
		}
	default:
		return providerDefaults{
			BaseURL: TypeSafeBaseURL,
			Model:   TypeSafeModel,
			KeyEnvs: []string{"TYPESAFE_API_KEY", "JEV_API_KEY", "OPENJEV_API_KEY"},
			BaseEnv: "TYPESAFE_BASE_URL",
		}
	}
}

func firstEnv(getenv func(string) string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// knownPublicHosts are first-party System One APIs we accept without DNS
// re-resolution at config time (still dialed over HTTPS via NoRedirectClient).
var knownPublicHosts = map[string]bool{
	"api.typesafe.ai": true,
	"api.openjev.sh":  true,
}

// validateBaseURL enforces SSRF-safe public HTTPS by default.
// When allowPrivate is true (JEV_ALLOW_PRIVATE_BASE=true), loopback/private
// http(s) bases are allowed for self-hosted OpenJev — never for request-body URLs.
func validateBaseURL(raw string, allowPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("url has no host")
	}
	if u.User != nil {
		return fmt.Errorf("userinfo in base URL is not allowed")
	}
	if knownPublicHosts[host] {
		if u.Scheme != "https" {
			return fmt.Errorf("url must use https")
		}
		return nil
	}
	if !allowPrivate {
		return httputil.ValidatePublicURL(raw, true)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url must use http or https")
	}
	// Still block cloud metadata hostnames by name.
	blocked := map[string]bool{
		"metadata.google.internal": true,
		"metadata":                 true,
		"169.254.169.254":          true,
	}
	if blocked[host] {
		return fmt.Errorf("host %q is not allowed", host)
	}
	return nil
}

func httpClientForBase(base string, allowPrivate bool, timeout time.Duration) *http.Client {
	if !allowPrivate {
		return httputil.NoRedirectClient(timeout)
	}
	// Private/self-hosted: no redirect, but dial may hit loopback.
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
		},
	}
}

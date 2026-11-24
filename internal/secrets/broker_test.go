package secrets

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAppRoleReadsFieldVersionAndNamespace(t *testing.T) {
	logins := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/approle/login", func(w http.ResponseWriter, r *http.Request) {
		logins++
		if r.Header.Get("X-Vault-Namespace") != "team" {
			t.Errorf("namespace header: %q", r.Header.Get("X-Vault-Namespace"))
		}
		var body struct {
			RoleID   string `json:"role_id"`
			SecretID string `json:"secret_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.RoleID != "role" || body.SecretID != "secret" {
			t.Fatalf("login body: %+v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"auth": map[string]any{"client_token": "s.tok", "lease_duration": 3600},
		})
	})
	mux.HandleFunc("/v1/secret/data/vaultrun/DB_PASSWORD", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("version") != "3" {
			t.Errorf("version: %q", r.URL.RawQuery)
		}
		if r.Header.Get("X-Vault-Token") != "s.tok" {
			t.Errorf("token header: %q", r.Header.Get("X-Vault-Token"))
		}
		if r.Header.Get("X-Vault-Namespace") != "team" {
			t.Errorf("namespace header: %q", r.Header.Get("X-Vault-Namespace"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"data": map[string]string{"password": "hunter2"}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	p, err := NewVaultProvider(VaultConfig{
		Addr: srv.URL, Namespace: "team", Field: "password", Version: "3",
		RoleID: "role", SecretID: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	p.client = srv.Client()
	for i := 0; i < 2; i++ {
		got, err := p.GetSecret(context.Background(), "DB_PASSWORD")
		if err != nil {
			t.Fatal(err)
		}
		if got != "hunter2" {
			t.Fatalf("secret: %q", got)
		}
	}
	if logins != 1 {
		t.Fatalf("approle logins: %d, want 1", logins)
	}
}

func TestVaultVersionMustBeDigits(t *testing.T) {
	_, err := NewVaultProvider(VaultConfig{Version: "1a"})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestStaticTokenSkipsAppRole(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/approle/login", func(w http.ResponseWriter, r *http.Request) {
		t.Error("approle login should not run when VAULT_TOKEN is set")
	})
	mux.HandleFunc("/v1/secret/data/vaultrun/DB_PASSWORD", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "static" {
			t.Errorf("token: %q", r.Header.Get("X-Vault-Token"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"data": map[string]string{"value": "x"}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p, err := NewVaultProvider(VaultConfig{Addr: srv.URL, Token: "static", RoleID: "role", SecretID: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	p.client = srv.Client()
	if _, err := p.GetSecret(context.Background(), "DB_PASSWORD"); err != nil {
		t.Fatal(err)
	}
}

func TestIMDSCredentialsAndRegion(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	resetIMDSCache()

	mux := http.NewServeMux()
	mux.HandleFunc("/latest/api/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("token method: %s", r.Method)
		}
		if r.Header.Get("X-aws-ec2-metadata-token-ttl-seconds") == "" {
			t.Error("missing token ttl")
		}
		_, _ = io.WriteString(w, "meta-token")
	})
	mux.HandleFunc("/latest/meta-data/iam/security-credentials/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-aws-ec2-metadata-token") != "meta-token" {
			t.Errorf("metadata token: %q", r.Header.Get("X-aws-ec2-metadata-token"))
		}
		if strings.HasSuffix(r.URL.Path, "/vaultrun-role") {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"Code":            "Success",
				"AccessKeyId":     "AKIA_TEST",
				"SecretAccessKey": "secret",
				"Token":           "session",
				"Expiration":      "2099-01-01T00:00:00Z",
			})
			return
		}
		_, _ = io.WriteString(w, "vaultrun-role\n")
	})
	mux.HandleFunc("/latest/meta-data/placement/region", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "eu-west-1")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	old := imdsBase
	imdsBase = srv.URL
	t.Cleanup(func() {
		imdsBase = old
		resetIMDSCache()
	})

	creds, err := awsCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessKeyID != "AKIA_TEST" || creds.SessionToken != "session" {
		t.Fatalf("creds: %+v", creds)
	}
	region, err := imdsRegion(context.Background())
	if err != nil || region != "eu-west-1" {
		t.Fatalf("region %q err %v", region, err)
	}
}

func TestEnvCredentialsSkipIMDS(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIA_ENV")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	old := imdsBase
	imdsBase = "http://127.0.0.1:1"
	t.Cleanup(func() { imdsBase = old })
	creds, err := awsCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessKeyID != "AKIA_ENV" {
		t.Fatalf("creds: %+v", creds)
	}
}

func TestIMDSClientRefusesOtherHosts(t *testing.T) {
	old := imdsBase
	imdsBase = "http://169.254.169.254"
	t.Cleanup(func() { imdsBase = old })
	_, err := imdsClient().Get("http://127.0.0.1:1/latest/api/token")
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("expected a refusal, got %v", err)
	}
}

func TestAzureClientCredentials(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/tenant/oauth2/v2.0/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("client_secret") != "secret" || r.Form.Get("grant_type") != "client_credentials" {
			t.Fatalf("form: %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "az-tok", "expires_in": 3600})
	})
	mux.HandleFunc("/secrets/DB_PASSWORD", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api-version") != "7.4" {
			t.Errorf("api-version: %q", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer az-tok" {
			t.Errorf("auth: %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"value": "hunter2"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	old := azureLoginBase
	azureLoginBase = srv.URL
	t.Cleanup(func() { azureLoginBase = old })

	p := NewAzureProvider(AzureConfig{
		VaultURL: srv.URL, TenantID: "tenant", ClientID: "client", ClientSecret: "secret",
	})
	p.client = srv.Client()
	got, err := p.GetSecret(context.Background(), "DB_PASSWORD")
	if err != nil || got != "hunter2" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestAzureManagedIdentity(t *testing.T) {
	resetIMDSCache()
	mux := http.NewServeMux()
	mux.HandleFunc("/metadata/identity/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata") != "true" {
			t.Error("missing Metadata header")
		}
		if r.URL.Query().Get("resource") != "https://vault.azure.net" {
			t.Errorf("resource: %q", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "mi-tok", "expires_in": 3600})
	})
	imds := httptest.NewServer(mux)
	t.Cleanup(imds.Close)
	vault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mi-tok" {
			t.Errorf("auth: %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"value": "from-mi"})
	}))
	t.Cleanup(vault.Close)
	old := imdsBase
	imdsBase = imds.URL
	t.Cleanup(func() { imdsBase = old })

	p := NewAzureProvider(AzureConfig{VaultURL: vault.URL, ClientID: "mi-client"})
	p.client = vault.Client()
	got, err := p.GetSecret(context.Background(), "DB_PASSWORD")
	if err != nil || got != "from-mi" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestAWSNotFoundIsDistinctFromOtherFailures(t *testing.T) {
	err := awsSecretError("DB", 400, []byte(`{"__type":"ResourceNotFoundException"}`))
	if !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
	err = awsSecretError("DB", 403, []byte(`{"__type":"AccessDeniedException"}`))
	if err == nil || IsNotFound(err) || IsInvalidName(err) {
		t.Fatalf("expected a backend error, got %v", err)
	}
}

func resetIMDSCache() {
	imdsCredMu.Lock()
	imdsCached = awsCreds{}
	imdsUntil = time.Time{}
	imdsCredMu.Unlock()
}

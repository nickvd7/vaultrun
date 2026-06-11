package secrets_test

import (
	"context"
	"os"
	"testing"

	"github.com/nickvd7/vaultrun/internal/secrets"
)

func TestEnvProviderFound(t *testing.T) {
	t.Setenv("VAULTRUN_SECRET_TESTKEY", "supersecret")
	p := &secrets.EnvProvider{}
	val, err := p.GetSecret(context.Background(), "TESTKEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "supersecret" {
		t.Errorf("want supersecret, got %q", val)
	}
}

func TestEnvProviderMissing(t *testing.T) {
	os.Unsetenv("VAULTRUN_SECRET_MISSING_KEY_XYZ")
	p := &secrets.EnvProvider{}
	_, err := p.GetSecret(context.Background(), "MISSING_KEY_XYZ")
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
}

func TestEnvProviderName(t *testing.T) {
	p := &secrets.EnvProvider{}
	if p.Name() != "env" {
		t.Errorf("want name=env, got %q", p.Name())
	}
}

func TestNewDefaultsToEnv(t *testing.T) {
	os.Unsetenv("SECRETS_PROVIDER")
	p := secrets.New()
	if p.Name() != "env" {
		t.Errorf("want env provider by default, got %q", p.Name())
	}
}

func TestNewVaultProvider(t *testing.T) {
	t.Setenv("SECRETS_PROVIDER", "vault")
	t.Setenv("VAULT_ADDR", "http://vault:8200")
	t.Setenv("VAULT_TOKEN", "root")
	p := secrets.New()
	if p.Name() != "vault" {
		t.Errorf("want vault provider, got %q", p.Name())
	}
}

func TestNewAWSProvider(t *testing.T) {
	t.Setenv("SECRETS_PROVIDER", "aws")
	t.Setenv("AWS_REGION", "us-east-1")
	p := secrets.New()
	if p.Name() != "aws" {
		t.Errorf("want aws provider, got %q", p.Name())
	}
}

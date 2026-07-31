package replay

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nickvd7/vaultrun/internal/models"
)

func testManager() *Manager {
	return &Manager{signingKey: []byte("test-signing-key-do-not-use-in-production")}
}

// validCheckpoint returns a fully populated checkpoint so each tamper test can
// mutate exactly one field.
func validCheckpoint() *Checkpoint {
	runID := uuid.New()
	exitCode := 0
	durationMs := 1500

	return &Checkpoint{
		ID:                  uuid.New(),
		SessionID:           uuid.New(),
		RunID:               &runID,
		CheckpointNumber:    3,
		Description:         "after running tests",
		WorkspaceSnapshotID: uuid.New(),
		ArchivePath:         "/vault/snapshots/abc.tar.gz",
		EnvVarsSnapshot:     models.JSONB{"PATH": "/usr/bin"},
		Command:             "pytest",
		Args:                models.JSONB{"0": "-v"},
		ExitCode:            &exitCode,
		DurationMs:          &durationMs,
		StdoutPreview:       "12 passed",
		StderrPreview:       "",
		CreatedAt:           time.Unix(1753900000, 0).UTC(),
		SizeBytes:           4096,
	}
}

func TestVerifyCheckpointAcceptsUntampered(t *testing.T) {
	m := testManager()
	cp := validCheckpoint()
	cp.Signature = m.signCheckpoint(cp)

	if !m.verifyCheckpoint(cp) {
		t.Error("verifyCheckpoint() = false for an untampered checkpoint, want true")
	}
}

// TestSignatureCoversExecutionContext is the regression test for the original
// scheme, which signed only SessionID, WorkspaceSnapshotID, CheckpointNumber
// and CreatedAt. Anyone with database access could rewrite the recorded
// command, exit code or captured output and the signature still verified.
func TestSignatureCoversExecutionContext(t *testing.T) {
	otherExit := 1
	otherDuration := 9999
	otherRun := uuid.New()

	tampers := []struct {
		field string
		apply func(*Checkpoint)
	}{
		{"SessionID", func(c *Checkpoint) { c.SessionID = uuid.New() }},
		{"WorkspaceSnapshotID", func(c *Checkpoint) { c.WorkspaceSnapshotID = uuid.New() }},
		{"CheckpointNumber", func(c *Checkpoint) { c.CheckpointNumber = 99 }},
		{"CreatedAt", func(c *Checkpoint) { c.CreatedAt = c.CreatedAt.Add(time.Hour) }},
		{"ArchivePath", func(c *Checkpoint) { c.ArchivePath = "/vault/snapshots/evil.tar.gz" }},
		{"Command", func(c *Checkpoint) { c.Command = "rm" }},
		{"Args", func(c *Checkpoint) { c.Args = models.JSONB{"0": "-rf", "1": "/"} }},
		{"ExitCode", func(c *Checkpoint) { c.ExitCode = &otherExit }},
		{"ExitCode cleared", func(c *Checkpoint) { c.ExitCode = nil }},
		{"DurationMs", func(c *Checkpoint) { c.DurationMs = &otherDuration }},
		{"RunID", func(c *Checkpoint) { c.RunID = &otherRun }},
		{"RunID cleared", func(c *Checkpoint) { c.RunID = nil }},
		{"StdoutPreview", func(c *Checkpoint) { c.StdoutPreview = "0 passed" }},
		{"StderrPreview", func(c *Checkpoint) { c.StderrPreview = "injected" }},
		{"SizeBytes", func(c *Checkpoint) { c.SizeBytes = 1 }},
		{"EnvVarsSnapshot", func(c *Checkpoint) { c.EnvVarsSnapshot = models.JSONB{"PATH": "/evil"} }},
	}

	for _, tc := range tampers {
		t.Run(tc.field, func(t *testing.T) {
			m := testManager()

			cp := validCheckpoint()
			cp.Signature = m.signCheckpoint(cp)

			tc.apply(cp)

			if m.verifyCheckpoint(cp) {
				t.Errorf("verifyCheckpoint() = true after tampering with %s — signature does not cover this field", tc.field)
			}
		})
	}
}

// TestVerifyCheckpointRejectsForgedSignature covers the case where an attacker
// supplies a signature rather than modifying a field.
func TestVerifyCheckpointRejectsForgedSignature(t *testing.T) {
	m := testManager()

	forged := []struct {
		name string
		sig  string
	}{
		{"empty", ""},
		{"all zeroes", strings.Repeat("0", 64)},
		{"not hex", strings.Repeat("z", 64)},
		{"truncated", "abcd"},
		{"too long", strings.Repeat("a", 128)},
		{"odd length hex", "abc"},
	}

	for _, tc := range forged {
		t.Run(tc.name, func(t *testing.T) {
			cp := validCheckpoint()
			cp.Signature = tc.sig

			if m.verifyCheckpoint(cp) {
				t.Errorf("verifyCheckpoint() = true for forged signature %q, want false", tc.sig)
			}
		})
	}
}

// TestVerifyCheckpointRejectsWrongKey: a checkpoint signed by one deployment
// must not verify against another's key.
func TestVerifyCheckpointRejectsWrongKey(t *testing.T) {
	signer := &Manager{signingKey: []byte("key-one")}
	verifier := &Manager{signingKey: []byte("key-two")}

	cp := validCheckpoint()
	cp.Signature = signer.signCheckpoint(cp)

	if verifier.verifyCheckpoint(cp) {
		t.Error("verifyCheckpoint() = true with a different signing key, want false")
	}
}

// TestVerifyCheckpointFailsClosedWithoutKey: a deployment that forgot to set
// AUDIT_HMAC_KEY must not silently treat every checkpoint as authentic.
func TestVerifyCheckpointFailsClosedWithoutKey(t *testing.T) {
	unkeyed := &Manager{signingKey: nil}

	cp := validCheckpoint()
	cp.Signature = unkeyed.signCheckpoint(cp)

	if unkeyed.verifyCheckpoint(cp) {
		t.Error("verifyCheckpoint() = true with no signing key configured, want false (fail closed)")
	}
}

// TestSignatureResistsBoundaryConfusion: fields are NUL-separated so that
// shifting a character across a field boundary changes the digest. Without a
// separator, ("ab", "c") and ("a", "bc") would hash identically.
func TestSignatureResistsBoundaryConfusion(t *testing.T) {
	m := testManager()

	a := validCheckpoint()
	a.Command = "ab"
	a.StdoutPreview = "c"

	b := validCheckpoint()
	b.ID = a.ID
	b.SessionID = a.SessionID
	b.RunID = a.RunID
	b.WorkspaceSnapshotID = a.WorkspaceSnapshotID
	b.CheckpointNumber = a.CheckpointNumber
	b.CreatedAt = a.CreatedAt
	b.ArchivePath = a.ArchivePath
	b.EnvVarsSnapshot = a.EnvVarsSnapshot
	b.Args = a.Args
	b.ExitCode = a.ExitCode
	b.DurationMs = a.DurationMs
	b.SizeBytes = a.SizeBytes
	b.Command = "a"
	b.StdoutPreview = "bc"

	if m.signCheckpoint(a) == m.signCheckpoint(b) {
		t.Error("signCheckpoint() produced identical digests for shifted field boundaries")
	}
}

// TestRedactEnvVarsCoversRealCredentialNames exercises the redaction list
// against names that appear in practice. The original list missed
// AWS_ACCESS_KEY_ID, SSH keys and connection strings.
func TestRedactEnvVarsCoversRealCredentialNames(t *testing.T) {
	m := testManager()

	secrets := []string{
		// AWS
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		// Other providers
		"GITHUB_TOKEN",
		"OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
		"STRIPE_SECRET_KEY",
		"GCP_SERVICE_ACCOUNT_KEY",
		"AZURE_CLIENT_SECRET",
		// Databases
		"DATABASE_PASSWORD",
		"DATABASE_URL_DSN",
		"POSTGRES_PASSWORD",
		"REDIS_PASSWORD",
		"MYSQL_PWD",
		"CONNECTION_STRING",
		// Crypto / auth material
		"SSH_PRIVATE_KEY",
		"TLS_CERT_KEY",
		"JWT_SIGNING_KEY",
		"SESSION_SECRET",
		"COOKIE_SECRET",
		"PASSWORD_SALT",
		"GPG_PASSPHRASE",
		"BASIC_AUTH_USER",
		"VAULTRUN_MASTER_KEY",
		"AUDIT_HMAC_KEY",
		// Lowercase and mixed case
		"my_api_key",
		"Database_Password",
	}

	env := make(map[string]string, len(secrets))
	for _, name := range secrets {
		env[name] = "super-secret-value"
	}

	result := m.redactEnvVars(env)

	for _, name := range secrets {
		got, ok := result[name]
		if !ok {
			t.Errorf("redactEnvVars() dropped key %q", name)
			continue
		}
		if got != "[REDACTED]" {
			t.Errorf("redactEnvVars() leaked %q as %v, want [REDACTED]", name, got)
		}
	}
}

// TestRedactEnvVarsPreservesNonSecrets: over-redaction that swallowed PATH or
// HOME would make checkpoints useless for debugging.
func TestRedactEnvVarsPreservesNonSecrets(t *testing.T) {
	m := testManager()

	env := map[string]string{
		"PATH":            "/usr/local/bin:/usr/bin",
		"HOME":            "/home/agent",
		"LANG":            "en_US.UTF-8",
		"PWD_OF_PROJECT":  "", // contains PWD — expected to be redacted, see below
		"PYTHONPATH":      "/app",
		"NODE_ENV":        "production",
		"TZ":              "UTC",
		"TERM":            "xterm",
		"VAULTRUN_REGION": "eu-west-1",
	}

	result := m.redactEnvVars(env)

	preserved := []string{"PATH", "HOME", "LANG", "PYTHONPATH", "NODE_ENV", "TZ", "TERM", "VAULTRUN_REGION"}
	for _, name := range preserved {
		if result[name] != env[name] {
			t.Errorf("redactEnvVars() redacted non-secret %q (got %v, want %q)", name, result[name], env[name])
		}
	}
}

func TestRedactEnvVarsHandlesNil(t *testing.T) {
	m := testManager()

	result := m.redactEnvVars(nil)
	if result == nil {
		t.Fatal("redactEnvVars(nil) = nil, want an empty JSONB")
	}
	if len(result) != 0 {
		t.Errorf("redactEnvVars(nil) has %d entries, want 0", len(result))
	}
}

// TestResourceLimitsAreBounded documents the ceilings that stop one session
// from consuming all checkpoint storage.
func TestResourceLimitsAreBounded(t *testing.T) {
	if MaxCheckpointsPerSession <= 0 {
		t.Error("MaxCheckpointsPerSession must be positive")
	}
	if MaxCheckpointSizeBytes <= 0 {
		t.Error("MaxCheckpointSizeBytes must be positive")
	}
	if MaxTotalCheckpointStoragePerOrg <= MaxCheckpointSizeBytes {
		t.Error("the per-org storage cap must exceed the single-checkpoint cap, otherwise no checkpoint fits")
	}
	if DefaultCheckpointLimit > MaxCheckpointsPerSession {
		t.Error("DefaultCheckpointLimit must not exceed MaxCheckpointsPerSession")
	}
}

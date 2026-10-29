// Package evidence builds and verifies session evidence packs: a record of
// everything a sandbox session did (configuration, commands and their output,
// workspace files, audit entries), signed with Ed25519 so that anyone holding
// the public key can check it offline, without access to the VaultRun server
// or any secret. Audit HMAC signatures only prove integrity to whoever holds
// AUDIT_HMAC_KEY; an evidence pack proves it to an auditor.
//
// The signature covers the exact bytes of the pack as exported, so verifying
// needs no JSON canonicalisation: Verify checks the bytes, then decodes them.
//
// This package has no database or Docker dependencies so the CLI can verify
// packs on a machine that has nothing but the pack and the public key.
package evidence

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// PackSchema identifies the evidence pack format.
const PackSchema = "vaultrun.evidence.pack.v1"

// SignatureAlg is the only signature algorithm packs use.
const SignatureAlg = "ed25519"

// MaxOutputBytes caps the stdout/stderr text embedded per run. The SHA-256 in
// the pack always covers the full stored output.
const MaxOutputBytes = 64 << 10

// Limits on what one pack holds; Notes records when one was reached.
const (
	MaxFiles        = 10000
	MaxRuns         = 10000
	MaxAuditEntries = 50000
)

// Audit scopes: which audit entries of the session a pack contains.
const (
	AuditScopeAll   = "all"   // every entry of the session (master key export)
	AuditScopeActor = "actor" // only the exporting key's own entries
)

// Envelope is the exported file: the pack bytes and their signature.
type Envelope struct {
	Pack      []byte    `json:"pack"` // JSON-encoded Pack; base64 in the file
	Signature Signature `json:"signature"`
}

// Signature is an Ed25519 signature over Envelope.Pack.
type Signature struct {
	Alg   string `json:"alg"`
	KeyID string `json:"key_id"`
	Value []byte `json:"value"`
}

// Pack is the signed content.
type Pack struct {
	Schema     string      `json:"schema"`
	ID         string      `json:"id"`
	CreatedAt  time.Time   `json:"created_at"`
	Issuer     Issuer      `json:"issuer"`
	ExportedBy string      `json:"exported_by"`
	Session    Session     `json:"session"`
	Runs       []Run       `json:"runs"`
	Files      []File      `json:"files"`
	Audit      AuditRecord `json:"audit"`
	// Notes says what the pack leaves out (limits reached, no workspace).
	Notes []string `json:"notes,omitempty"`
}

// Issuer identifies the VaultRun instance that exported the pack.
type Issuer struct {
	Product string `json:"product"`
	Version string `json:"version"`
}

// Session is the sandbox configuration the commands ran under.
type Session struct {
	ID             string     `json:"id"`
	Name           string     `json:"name,omitempty"`
	Image          string     `json:"image"`
	Runtime        string     `json:"runtime,omitempty"` // from the signed session.created entry; empty = not recorded (daemon default)
	Status         string     `json:"status"`
	NetworkEnabled bool       `json:"network_enabled"`
	AllowedHosts   []string   `json:"allowed_hosts,omitempty"`
	CPULimit       float64    `json:"cpu_limit"`
	MemoryLimitMB  int        `json:"memory_limit_mb"`
	TimeoutSeconds int        `json:"timeout_seconds"`
	CreatedBy      string     `json:"created_by"`
	CreatedByName  string     `json:"created_by_name,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	StoppedAt      *time.Time `json:"stopped_at,omitempty"`
}

// Run is one command executed in the session.
type Run struct {
	ID              string     `json:"id"`
	Command         string     `json:"command"`
	Args            []string   `json:"args"`
	WorkingDir      string     `json:"working_dir,omitempty"`
	Status          string     `json:"status"`
	ExitCode        *int       `json:"exit_code,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	DurationMS      *int64     `json:"duration_ms,omitempty"`
	Stdout          string     `json:"stdout"`
	Stderr          string     `json:"stderr"`
	StdoutSHA256    string     `json:"stdout_sha256"`
	StderrSHA256    string     `json:"stderr_sha256"`
	StdoutTruncated bool       `json:"stdout_truncated,omitempty"`
	StderrTruncated bool       `json:"stderr_truncated,omitempty"`
}

// File is a workspace file at export time.
type File struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256,omitempty"` // empty for symlinks
	Symlink bool   `json:"symlink,omitempty"`
}

// AuditRecord holds the session's audit entries and how they were selected.
type AuditRecord struct {
	Scope   string       `json:"scope"`
	Signed  bool         `json:"signed"` // AUDIT_HMAC_KEY was set, so SigStatus was verified at export
	Entries []AuditEntry `json:"entries"`
}

// AuditEntry is one audit log entry as verified by the server at export.
type AuditEntry struct {
	ID        string         `json:"id"`
	Seq       *int64         `json:"seq,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Actor     string         `json:"actor"`
	ActorName string         `json:"actor_name,omitempty"`
	Action    string         `json:"action"`
	RunID     string         `json:"run_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Sig       string         `json:"sig,omitempty"`
	SigStatus string         `json:"sig_status,omitempty"`
	SigReason string         `json:"sig_reason,omitempty"`
}

// KeyID names a public key: the first 16 hex characters of its SHA-256.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// SHA256Hex returns the hex SHA-256 of s.
func SHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

package verify

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// EvidenceSchema identifies the sealed verification export format.
const EvidenceSchema = "vaultrun.verify.evidence.v1"

// MaxEvidenceStdoutBytes caps observation text embedded in evidence exports.
const MaxEvidenceStdoutBytes = 64 << 10

// EvidenceKind classifies what the record contains.
type EvidenceKind string

const (
	EvidenceKindCheckpoint EvidenceKind = "checkpoint"
	EvidenceKindControls   EvidenceKind = "controls"
	EvidenceKindCombined   EvidenceKind = "combined"
)

// EvidenceSubject binds the record to optional VaultRun entities.
type EvidenceSubject struct {
	SessionID      string `json:"session_id,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	VerificationID string `json:"verification_id,omitempty"`
	StepName       string `json:"step_name,omitempty"`
	Actor          string `json:"actor,omitempty"`
}

// EvidenceCheckpoint is one evaluated verify checkpoint inside an export.
type EvidenceCheckpoint struct {
	Spec        Spec        `json:"spec"`
	Observation Observation `json:"observation"`
	Checks      []Check     `json:"checks"`
	Passed      bool        `json:"passed"`
}

// EvidenceRecord is a digests-and-optional-HMAC sealed verification export
// (FaultWright-style evidence packaging for VaultRun verify).
type EvidenceRecord struct {
	Schema        string              `json:"schema"`
	ID            string              `json:"id"`
	CreatedAt     time.Time           `json:"created_at"`
	Kind          EvidenceKind        `json:"kind"`
	Passed        bool                `json:"passed"`
	Subject       EvidenceSubject     `json:"subject"`
	Checkpoint    *EvidenceCheckpoint `json:"checkpoint,omitempty"`
	Controls      *ControlReport      `json:"controls,omitempty"`
	ContentDigest string              `json:"content_digest"`
	Sig           string              `json:"sig,omitempty"`
}

// EvidenceInput configures BuildEvidence.
type EvidenceInput struct {
	Actor            string
	SessionID        *uuid.UUID
	RunID            *uuid.UUID
	VerificationID   *uuid.UUID
	StepName         string
	Spec             Spec
	Observation      Observation
	Result           Result
	IncludeControls  bool
	HasCheckpoint    bool
	HMACKey          []byte
}

// BuildEvidence seals a verification export with content digest and optional HMAC.
func BuildEvidence(in EvidenceInput) (*EvidenceRecord, error) {
	if !in.HasCheckpoint && !in.IncludeControls {
		return nil, fmt.Errorf("evidence requires a checkpoint and/or include_controls")
	}
	if utf8.RuneCountInString(in.StepName) > 200 {
		return nil, fmt.Errorf("step_name too long")
	}
	if utf8.RuneCountInString(in.Actor) > 256 {
		return nil, fmt.Errorf("actor too long")
	}

	rec := &EvidenceRecord{
		Schema:    EvidenceSchema,
		ID:        uuid.NewString(),
		CreatedAt: time.Now().UTC(),
		Subject: EvidenceSubject{
			StepName: strings.TrimSpace(in.StepName),
			Actor:    strings.TrimSpace(in.Actor),
		},
	}
	if in.SessionID != nil {
		rec.Subject.SessionID = in.SessionID.String()
	}
	if in.RunID != nil {
		rec.Subject.RunID = in.RunID.String()
	}
	if in.VerificationID != nil {
		rec.Subject.VerificationID = in.VerificationID.String()
	}

	passed := true
	if in.HasCheckpoint {
		obs := sanitizeObservation(in.Observation)
		rec.Checkpoint = &EvidenceCheckpoint{
			Spec:        in.Spec,
			Observation: obs,
			Checks:      in.Result.Checks,
			Passed:      in.Result.Passed,
		}
		if !in.Result.Passed {
			passed = false
		}
	}
	if in.IncludeControls {
		report := RunControls()
		// Freeze CreatedAt for digest stability within this record build by
		// copying outcomes; re-stamp report time to rec.CreatedAt.
		report.CreatedAt = rec.CreatedAt
		rec.Controls = &report
		if !report.Passed {
			passed = false
		}
	}

	switch {
	case in.HasCheckpoint && in.IncludeControls:
		rec.Kind = EvidenceKindCombined
	case in.IncludeControls:
		rec.Kind = EvidenceKindControls
	default:
		rec.Kind = EvidenceKindCheckpoint
	}
	rec.Passed = passed

	digest, err := ComputeContentDigest(rec)
	if err != nil {
		return nil, err
	}
	rec.ContentDigest = digest
	if len(in.HMACKey) > 0 {
		rec.Sig = SignEvidence(digest, in.HMACKey)
	}
	return rec, nil
}

// ComputeContentDigest returns SHA-256 hex of the canonical JSON payload
// with content_digest and sig cleared.
func ComputeContentDigest(rec *EvidenceRecord) (string, error) {
	if rec == nil {
		return "", fmt.Errorf("nil evidence")
	}
	clone := *rec
	clone.ContentDigest = ""
	clone.Sig = ""
	raw, err := json.Marshal(clone)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// SignEvidence HMAC-SHA256s the content digest hex string.
func SignEvidence(contentDigest string, key []byte) string {
	if len(key) == 0 || contentDigest == "" {
		return ""
	}
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(contentDigest))
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyEvidenceIntegrity checks content_digest (and sig when key provided).
func VerifyEvidenceIntegrity(rec *EvidenceRecord, hmacKey []byte) error {
	if rec == nil {
		return fmt.Errorf("nil evidence")
	}
	if rec.Schema != EvidenceSchema {
		return fmt.Errorf("unsupported schema %q", rec.Schema)
	}
	want, err := ComputeContentDigest(rec)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(rec.ContentDigest)) != 1 {
		return fmt.Errorf("content_digest mismatch")
	}
	if len(hmacKey) > 0 {
		if rec.Sig == "" {
			return fmt.Errorf("sig missing")
		}
		expect := SignEvidence(rec.ContentDigest, hmacKey)
		if subtle.ConstantTimeCompare([]byte(expect), []byte(rec.Sig)) != 1 {
			return fmt.Errorf("sig mismatch")
		}
	}
	return nil
}

func sanitizeObservation(obs Observation) Observation {
	out := obs
	out.Stdout = truncateEvidenceBytes(out.Stdout, MaxEvidenceStdoutBytes)
	out.Stderr = truncateEvidenceBytes(out.Stderr, MaxEvidenceStdoutBytes)
	// Strip NULs that break JSON consumers / logs.
	out.Stdout = strings.ReplaceAll(out.Stdout, "\x00", "")
	out.Stderr = strings.ReplaceAll(out.Stderr, "\x00", "")
	return out
}

func truncateEvidenceBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

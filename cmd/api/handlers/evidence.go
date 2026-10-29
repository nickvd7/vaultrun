package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/evidence"
	"github.com/nickvd7/vaultrun/internal/models"
	"github.com/nickvd7/vaultrun/internal/version"
)

// EvidenceHandler exports signed session evidence packs.
type EvidenceHandler struct {
	h *Hub
}

func NewEvidenceHandler(h *Hub) *EvidenceHandler { return &EvidenceHandler{h: h} }

const evidenceNotConfigured = "evidence export is not configured: set EVIDENCE_SIGNING_KEY (see docs/features/evidence-packs.md)"

// GET /api/v1/evidence/public-key
// The key an auditor verifies packs with. It is not secret.
func (eh *EvidenceHandler) PublicKey(c *gin.Context) {
	s := eh.h.evidenceSigner
	if s == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": evidenceNotConfigured})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"algorithm":      evidence.SignatureAlg,
		"key_id":         s.KeyID(),
		"public_key_pem": string(s.PublicKeyPEM()),
	})
}

// GET /api/v1/sessions/:id/evidence
// Builds and signs an evidence pack for the session: its configuration, every
// run with output hashes, the workspace files with hashes, and its audit
// entries as verified now. A regular key gets its own audit entries only, the
// same scope as GET /audit; the pack records which scope it has.
func (eh *EvidenceHandler) Export(c *gin.Context) {
	s := eh.h.evidenceSigner
	if s == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": evidenceNotConfigured})
		return
	}
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	session, ok := eh.h.checkSessionAccess(c, id, models.OrgRoleViewer)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	actor := middleware.Actor(c)

	pack := &evidence.Pack{
		Schema:     evidence.PackSchema,
		ID:         uuid.NewString(),
		CreatedAt:  time.Now().UTC(),
		Issuer:     evidence.Issuer{Product: "vaultrun", Version: version.Version()},
		ExportedBy: actor,
		Session: evidence.Session{
			ID:             session.ID.String(),
			Image:          session.Image,
			Status:         session.Status,
			NetworkEnabled: session.NetworkEnabled,
			AllowedHosts:   []string(session.AllowedHosts),
			CPULimit:       session.CPULimit,
			MemoryLimitMB:  session.MemoryLimitMB,
			TimeoutSeconds: session.TimeoutSeconds,
			CreatedBy:      session.CreatedBy,
			CreatedAt:      session.CreatedAt.UTC(),
			StoppedAt:      session.StoppedAt,
		},
	}
	if session.Name != nil {
		pack.Session.Name = *session.Name
	}

	runs, err := eh.runs(c, id, pack)
	if err != nil {
		return
	}
	pack.Runs = runs

	files, truncated, err := evidence.HashWorkspace(eh.h.ws.SessionPath(id))
	if err != nil {
		slog.Error("evidence: hash workspace", "session_id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read workspace files"})
		return
	}
	pack.Files = files
	if truncated {
		pack.Notes = append(pack.Notes, fmt.Sprintf("only the first %d workspace files are listed", evidence.MaxFiles))
	}
	if session.Status != models.SessionStatusRunning && len(files) == 0 {
		pack.Notes = append(pack.Notes, "the session's workspace was removed; files are not listed")
	}

	if err := eh.audit(c, id, actor, pack); err != nil {
		return
	}

	names := []string{session.CreatedBy}
	for _, e := range pack.Audit.Entries {
		names = append(names, e.Actor)
	}
	if n, err := dbpkg.APIKeyNames(ctx, eh.h.db, names); err == nil {
		pack.Session.CreatedByName = n[session.CreatedBy]
		for i := range pack.Audit.Entries {
			pack.Audit.Entries[i].ActorName = n[pack.Audit.Entries[i].Actor]
		}
	}

	env, err := s.Sign(pack)
	if err != nil {
		slog.Error("evidence: sign", "session_id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to sign evidence pack"})
		return
	}
	body, err := json.Marshal(env)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode evidence pack"})
		return
	}

	eh.h.audit.Log(ctx, audit.Event{
		Actor:     actor,
		SessionID: &id,
		Action:    models.ActionEvidenceExported,
		Metadata: models.JSONB{
			"pack_id":       pack.ID,
			"key_id":        s.KeyID(),
			"runs":          len(pack.Runs),
			"files":         len(pack.Files),
			"audit_entries": len(pack.Audit.Entries),
			"audit_scope":   pack.Audit.Scope,
		},
	})

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="vaultrun-evidence-%s.json"`, id))
	c.Data(http.StatusOK, "application/json", body)
}

// runs returns the session's runs, oldest first, with output hashes.
func (eh *EvidenceHandler) runs(c *gin.Context, id uuid.UUID, pack *evidence.Pack) ([]evidence.Run, error) {
	const page = 100
	out := []evidence.Run{}
	for offset := 0; ; offset += page {
		rows, err := dbpkg.ListRuns(c.Request.Context(), eh.h.db, id, page, offset)
		if err != nil {
			slog.Error("evidence: list runs", "session_id", id, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list runs"})
			return nil, err
		}
		for _, r := range rows {
			if len(out) == evidence.MaxRuns {
				pack.Notes = append(pack.Notes, fmt.Sprintf("only the newest %d runs are included", evidence.MaxRuns))
				slices.Reverse(out)
				return out, nil
			}
			out = append(out, evidence.NewRun(evidence.Run{
				ID:         r.ID.String(),
				Command:    r.Command,
				Args:       []string(r.Args),
				WorkingDir: r.WorkingDir,
				Status:     r.Status,
				ExitCode:   r.ExitCode,
				CreatedAt:  r.CreatedAt.UTC(),
				StartedAt:  r.StartedAt,
				FinishedAt: r.FinishedAt,
				DurationMS: r.DurationMS,
			}, deref(r.Stdout), deref(r.Stderr)))
		}
		if len(rows) < page {
			break
		}
	}
	// ListRuns is newest first; a record reads in the order things happened.
	slices.Reverse(out)
	return out, nil
}

// audit fills pack.Audit with the session's entries (verified by the server
// now) and pack.Session.Runtime from the signed session.created entry.
func (eh *EvidenceHandler) audit(c *gin.Context, id uuid.UUID, actor string, pack *evidence.Pack) error {
	ctx := c.Request.Context()
	filter := dbpkg.AuditFilter{SessionID: &id, BySeq: true}
	pack.Audit.Scope = evidence.AuditScopeAll
	if actor != "master" {
		filter.Actor = actor
		pack.Audit.Scope = evidence.AuditScopeActor
	}

	const page = 500
	var logs []*models.AuditLog
	for offset := 0; ; offset += page {
		rows, err := dbpkg.ListAuditLogsFiltered(ctx, eh.h.db, filter, page, offset)
		if err != nil {
			slog.Error("evidence: list audit", "session_id", id, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list audit entries"})
			return err
		}
		logs = append(logs, rows...)
		if len(logs) >= evidence.MaxAuditEntries {
			logs = logs[:evidence.MaxAuditEntries]
			pack.Notes = append(pack.Notes, fmt.Sprintf("only the newest %d audit entries are included", evidence.MaxAuditEntries))
			break
		}
		if len(rows) < page {
			break
		}
	}
	pack.Audit.Signed = eh.h.audit != nil && eh.h.audit.Enabled()
	if pack.Audit.Signed {
		eh.h.audit.Classify(ctx, logs)
	}
	slices.Reverse(logs) // oldest first

	pack.Audit.Entries = make([]evidence.AuditEntry, 0, len(logs))
	for _, l := range logs {
		e := evidence.AuditEntry{
			ID:        l.ID.String(),
			Seq:       l.Seq,
			Timestamp: l.Timestamp.UTC(),
			Actor:     l.Actor,
			Action:    l.Action,
			Metadata:  l.Metadata,
			Sig:       l.Sig,
			SigStatus: l.SigStatus,
			SigReason: l.SigReason,
		}
		if l.RunID != nil {
			e.RunID = l.RunID.String()
		}
		pack.Audit.Entries = append(pack.Audit.Entries, e)
	}

	// The runtime is recorded in session.created, which a regular key may not
	// see when another key created the session; read it regardless.
	created, err := dbpkg.ListAuditLogsFiltered(ctx, eh.h.db,
		dbpkg.AuditFilter{SessionID: &id, ActionPrefix: models.ActionSessionCreated}, 10, 0)
	if err == nil {
		for _, l := range created {
			if l.Action != models.ActionSessionCreated {
				continue
			}
			if rt, ok := l.Metadata["runtime"].(string); ok {
				pack.Session.Runtime = rt
			}
		}
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

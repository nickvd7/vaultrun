// Package siemexport provides a background goroutine that continuously exports
// new audit log entries to an external SIEM or webhook endpoint.
//
// Activation: set AUDIT_EXPORT_URL to an HTTP(S) endpoint. The exporter reads
// entries created after the last successfully-exported timestamp and POSTs them
// as newline-delimited JSON (one entry per line, content-type application/x-ndjson).
// An optional AUDIT_EXPORT_SECRET sets a Bearer token on the Authorization header.
//
// The exporter starts from the most-recent audit entry at startup (no historical
// re-export on first run) and advances its bookmark atomically after each
// successful delivery.
package siemexport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"

	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/httputil"
)

// Exporter polls for new audit entries and ships them to an HTTP endpoint.
type Exporter struct {
	db         *sqlx.DB
	exportURL  string
	secret     string
	httpClient *http.Client
	bookmark   atomic.Value // stores time.Time
	interval   time.Duration
}

// New creates an Exporter from environment variables.
// Returns nil and logs a message when AUDIT_EXPORT_URL is not set.
func New(db *sqlx.DB) *Exporter {
	exportURL := os.Getenv("AUDIT_EXPORT_URL")
	if exportURL == "" {
		return nil
	}
	// Validate the export URL at startup to catch misconfigurations early.
	if err := httputil.ValidatePublicURL(exportURL, false); err != nil {
		slog.Warn("AUDIT_EXPORT_URL validation failed — exporter disabled",
			"url", exportURL, "err", err)
		return nil
	}
	e := &Exporter{
		db:         db,
		exportURL:  exportURL,
		secret:     os.Getenv("AUDIT_EXPORT_SECRET"),
		httpClient: httputil.NoRedirectClient(15 * time.Second),
		interval:   30 * time.Second,
	}
	// Start from now — don't re-export historical entries on first run.
	e.bookmark.Store(time.Now().UTC())
	return e
}

// Start launches the background export loop. It returns immediately; call
// cancel on the context to stop it.
func (e *Exporter) Start(ctx context.Context) {
	go e.loop(ctx)
}

func (e *Exporter) loop(ctx context.Context) {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.export(ctx); err != nil {
				slog.Warn("siem export failed", "err", err)
			}
		}
	}
}

func (e *Exporter) export(ctx context.Context) error {
	since := e.bookmark.Load().(time.Time)

	entries, err := dbpkg.ListAuditSince(ctx, e.db, since, 500)
	if err != nil {
		return fmt.Errorf("list audit entries: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.exportURL, &buf)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if e.secret != "" {
		req.Header.Set("Authorization", "Bearer "+e.secret)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post to siem: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("siem returned %d", resp.StatusCode)
	}

	// Advance bookmark to just after the last exported entry.
	last := entries[len(entries)-1]
	e.bookmark.Store(last.Timestamp.Add(time.Nanosecond))
	slog.Info("siem export succeeded", "count", len(entries), "endpoint", e.exportURL)
	return nil
}

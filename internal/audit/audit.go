package audit

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
)

type Logger struct {
	db *sqlx.DB
}

func New(db *sqlx.DB) *Logger {
	return &Logger{db: db}
}

type Event struct {
	Actor     string
	SessionID *uuid.UUID
	RunID     *uuid.UUID
	Action    string
	Metadata  models.JSONB
}

func (l *Logger) Log(ctx context.Context, e Event) {
	log := &models.AuditLog{
		ID:        uuid.New(),
		Timestamp: time.Now().UTC(),
		Actor:     e.Actor,
		SessionID: e.SessionID,
		RunID:     e.RunID,
		Action:    e.Action,
		Metadata:  e.Metadata,
	}
	if log.Metadata == nil {
		log.Metadata = models.JSONB{}
	}

	if err := dbpkg.CreateAuditLog(ctx, l.db, log); err != nil {
		slog.Error("audit log write failed", "action", e.Action, "err", err)
	}
}

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
)

const keyLength = 32

// GenerateKey creates a cryptographically random API key with a readable prefix.
// Returns the plaintext key (shown once) and a models.APIKey ready to persist.
// expiresAt is optional; pass nil for a non-expiring key.
func GenerateKey(name string, expiresAt *time.Time) (plaintext string, key *models.APIKey, err error) {
	raw := make([]byte, keyLength)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate random bytes: %w", err)
	}

	plaintext = "vr_" + hex.EncodeToString(raw)
	prefix := plaintext[:8]
	hash := sha256Key(plaintext)

	key = &models.APIKey{
		ID:        uuid.New(),
		Name:      name,
		KeyHash:   hash,
		Prefix:    prefix,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: expiresAt,
		Active:    true,
	}

	return plaintext, key, nil
}

// HashKey returns the SHA-256 hex digest of a plaintext key.
func HashKey(plaintext string) string {
	return sha256Key(plaintext)
}

func sha256Key(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Validate looks up and validates the key, returning the matching APIKey record.
// It updates last_used_at as a side effect.
func Validate(ctx context.Context, db *sqlx.DB, plaintext string) (*models.APIKey, error) {
	hash := sha256Key(plaintext)

	key, err := dbpkg.GetAPIKeyByHash(ctx, db, hash)
	if err != nil {
		return nil, fmt.Errorf("invalid api key")
	}

	if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("api key expired")
	}

	// best-effort last_used update
	_ = dbpkg.UpdateAPIKeyLastUsed(ctx, db, key.ID)

	return key, nil
}

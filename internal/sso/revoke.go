package sso

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RevocationStore tracks invalidated session JTI values so that explicitly
// logged-out JWTs cannot be reused before their natural expiry.
type RevocationStore interface {
	Revoke(ctx context.Context, jti string, ttl time.Duration) error
	IsRevoked(ctx context.Context, jti string) bool
}

// RedisRevocationStore persists revoked JTI values in Redis with automatic TTL expiry.
type RedisRevocationStore struct {
	rdb    *redis.Client
	prefix string
}

func NewRedisRevocationStore(addr, password string, db int) *RedisRevocationStore {
	return &RedisRevocationStore{
		rdb:    redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db}),
		prefix: "sso:revoke:",
	}
}

func (s *RedisRevocationStore) Revoke(ctx context.Context, jti string, ttl time.Duration) error {
	return s.rdb.Set(ctx, s.prefix+jti, "1", ttl).Err()
}

func (s *RedisRevocationStore) IsRevoked(ctx context.Context, jti string) bool {
	n, _ := s.rdb.Exists(ctx, s.prefix+jti).Result()
	return n > 0
}

// Package session stores admin sessions in Redis so they are shared by every
// replica and can be revoked on logout.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrNotFound = errors.New("session not found")

type Store struct {
	Redis *redis.Client
	TTL   time.Duration
}

// Only a hash of the token is stored, so a Redis dump does not leak live sessions.
func key(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "session:" + hex.EncodeToString(sum[:])
}

func (s *Store) Create(ctx context.Context, subject string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	if err := s.Redis.Set(ctx, key(token), subject, s.TTL).Err(); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) Lookup(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", ErrNotFound
	}
	subject, err := s.Redis.Get(ctx, key(token)).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrNotFound
	}
	return subject, err
}

func (s *Store) Delete(ctx context.Context, token string) error {
	return s.Redis.Del(ctx, key(token)).Err()
}

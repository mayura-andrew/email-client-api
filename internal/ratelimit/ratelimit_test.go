package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedis(t *testing.T) *redis.Client {
	t.Helper()
	server := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: server.Addr()})
}

func TestAllowEnforcesBurstThenRate(t *testing.T) {
	limiter := &Limiter{Redis: newRedis(t), Prefix: "rl:"}
	ctx := context.Background()
	limit := Limit{Rate: 1, Burst: 3}

	for i := 0; i < 3; i++ {
		ok, _, err := limiter.Allow(ctx, "ip", limit)
		if err != nil || !ok {
			t.Fatalf("request %d within burst: ok=%v err=%v", i, ok, err)
		}
	}
	ok, retryAfter, err := limiter.Allow(ctx, "ip", limit)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("request beyond burst was allowed")
	}
	if retryAfter <= 0 || retryAfter > time.Second {
		t.Fatalf("retryAfter = %v, want (0, 1s]", retryAfter)
	}

	ok, _, _ = limiter.Allow(ctx, "other-ip", limit)
	if !ok {
		t.Fatal("keys must be limited independently")
	}
}

func TestQuotaStopsAtDailyLimit(t *testing.T) {
	quota := &Quota{Redis: newRedis(t), Prefix: "q:", Daily: 2}
	ctx := context.Background()
	now := time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)

	for i := 0; i < 2; i++ {
		if ok, _, err := quota.Take(ctx, now); err != nil || !ok {
			t.Fatalf("take %d: ok=%v err=%v", i, ok, err)
		}
	}
	ok, reset, err := quota.Take(ctx, now)
	if err != nil || ok {
		t.Fatalf("third take: ok=%v err=%v", ok, err)
	}
	if want := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC); !reset.Equal(want) {
		t.Fatalf("reset = %v, want %v", reset, want)
	}

	quota.Release(ctx, now)
	if ok, _, _ := quota.Take(ctx, now); !ok {
		t.Fatal("released unit should be reusable")
	}
	if ok, _, _ := quota.Take(ctx, now.Add(24*time.Hour)); !ok {
		t.Fatal("quota should reset the next day")
	}
}

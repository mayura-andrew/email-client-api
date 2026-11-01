// Package ratelimit provides a GCRA (generic cell rate algorithm) limiter whose
// state lives in Redis, so every replica shares a single budget per key.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// The stored value is the theoretical arrival time (TAT) in milliseconds.
// Redis TIME is used instead of the caller's clock so replicas with skewed
// clocks still agree.
var gcra = redis.NewScript(`
local interval = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local tat = tonumber(redis.call('GET', KEYS[1]))
if not tat or tat < now then tat = now end
local new_tat = tat + interval
local allow_at = new_tat - burst * interval
if now < allow_at then
  return {0, math.ceil(allow_at - now)}
end
redis.call('SET', KEYS[1], string.format('%.3f', new_tat), 'PX', math.ceil(new_tat - now) + 1000)
return {1, 0}
`)

type Limiter struct {
	Redis  *redis.Client
	Prefix string
}

type Limit struct {
	// Rate is the sustained number of events per second.
	Rate float64
	// Burst is how many events may happen back to back.
	Burst int
}

func PerMinute(n int, burst int) Limit { return Limit{Rate: float64(n) / 60, Burst: burst} }

// Allow consumes one token for key and reports how long to wait when denied.
func (l *Limiter) Allow(ctx context.Context, key string, limit Limit) (bool, time.Duration, error) {
	if limit.Rate <= 0 {
		return true, 0, nil
	}
	if limit.Burst < 1 {
		limit.Burst = 1
	}
	interval := 1000 / limit.Rate
	result, err := gcra.Run(ctx, l.Redis, []string{l.Prefix + key}, interval, limit.Burst).Int64Slice()
	if err != nil {
		return false, 0, err
	}
	if len(result) != 2 {
		return false, 0, fmt.Errorf("ratelimit: unexpected script result %v", result)
	}
	return result[0] == 1, time.Duration(result[1]) * time.Millisecond, nil
}

// Wait blocks until a token for key is available or ctx is done.
func (l *Limiter) Wait(ctx context.Context, key string, limit Limit) error {
	for {
		ok, retryAfter, err := l.Allow(ctx, key, limit)
		if err != nil || ok {
			return err
		}
		timer := time.NewTimer(retryAfter)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Quota is a fixed daily allowance (e.g. Gmail's 500 or 2,000 messages/day),
// reset at UTC midnight.
type Quota struct {
	Redis  *redis.Client
	Prefix string
	Daily  int
}

var takeQuota = redis.NewScript(`
local used = tonumber(redis.call('GET', KEYS[1]) or '0')
if used >= tonumber(ARGV[1]) then return 0 end
redis.call('INCR', KEYS[1])
redis.call('EXPIRE', KEYS[1], 172800)
return 1
`)

// Take consumes one unit of today's quota. When the quota is spent it returns
// false and the time the quota resets.
func (q *Quota) Take(ctx context.Context, now time.Time) (bool, time.Time, error) {
	now = now.UTC()
	reset := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	if q.Daily <= 0 {
		return true, reset, nil
	}
	ok, err := takeQuota.Run(ctx, q.Redis, []string{q.Prefix + now.Format("2006-01-02")}, q.Daily).Int()
	if err != nil {
		return false, reset, err
	}
	return ok == 1, reset, nil
}

// Release returns a unit taken by Take when the send did not happen.
func (q *Quota) Release(ctx context.Context, now time.Time) {
	if q.Daily <= 0 {
		return
	}
	_ = q.Redis.Decr(ctx, q.Prefix+now.UTC().Format("2006-01-02")).Err()
}

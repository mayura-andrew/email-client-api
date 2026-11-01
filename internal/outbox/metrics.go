package outbox

import (
	"expvar"
	"sync"
	"sync/atomic"
)

// Metrics are published under /debug/vars as "outbox".
type Metrics struct {
	Published  atomic.Int64
	Sent       atomic.Int64
	Retried    atomic.Int64
	Deferred   atomic.Int64
	Failed     atomic.Int64
	Duplicates atomic.Int64
	Reclaimed  atomic.Int64
	SendMillis atomic.Int64
	depthMu    sync.Mutex
	depth      map[string]int64
}

var metrics = newMetrics()

func newMetrics() *Metrics {
	m := &Metrics{depth: map[string]int64{}}
	expvar.Publish("outbox", expvar.Func(func() any {
		m.depthMu.Lock()
		depth := make(map[string]int64, len(m.depth))
		for k, v := range m.depth {
			depth[k] = v
		}
		m.depthMu.Unlock()
		sent := m.Sent.Load()
		avg := int64(0)
		if sent > 0 {
			avg = m.SendMillis.Load() / sent
		}
		return map[string]any{
			"published":          m.Published.Load(),
			"sent":               sent,
			"retried":            m.Retried.Load(),
			"deferred":           m.Deferred.Load(),
			"failed":             m.Failed.Load(),
			"duplicates_skipped": m.Duplicates.Load(),
			"reclaimed":          m.Reclaimed.Load(),
			"avg_send_ms":        avg,
			"depth":              depth,
		}
	}))
	return m
}

func (m *Metrics) setDepth(depth map[string]int64) {
	m.depthMu.Lock()
	m.depth = depth
	m.depthMu.Unlock()
}
